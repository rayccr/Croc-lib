// library.go
//
// 本文件是 Croc 核心面向 GUI/SDK 的最小库化适配层，不改变原有 CLI 行为。
// 这里只增加可选的进度观测、交互回调、终端输出隔离和独立接收目录能力。
// CLI 未设置这些字段时，仍走原来的终端路径。
package croc

import (
	"context"
	"io"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/schollz/croc/v11/src/termui"
	"github.com/schollz/croc/v11/src/utils"
)

// PromptKind 标识库模式下的交互类型。C++ 侧根据该字段决定如何展示。
type PromptKind string

const (
	// PromptReceiveAccept 接收前的文件清单确认。
	PromptReceiveAccept PromptKind = "receive_accept"
	// PromptOverwrite 已存在文件的覆盖/续传确认。
	PromptOverwrite PromptKind = "overwrite"
	// PromptFolderOverwrite 已存在非空目录的覆盖确认。
	PromptFolderOverwrite PromptKind = "folder_overwrite"
	// PromptSendApproval 发送端在对方请求文件后的确认（--ask 模式）。
	PromptSendApproval PromptKind = "send_approval"
)

// PromptRequest 是跨语言交互请求的只读描述。所有字符串均为普通 Go 字符串，
// 由库化桥接层转换为 UTF-8 JSON 后交给 C++，不会直接把 Go 指针暴露给 C。
type PromptRequest struct {
	Kind          PromptKind
	FileIndex     int
	FileName      string
	Destination   string
	FileCount     int
	TotalBytes    int64
	FileBytes     int64
	ResumePercent float64
	PeerID        string
	DefaultYes    bool
}

// PromptAnswer 是用户决定。Accepted 为 false 表示拒绝/跳过。
type PromptAnswer struct {
	Accepted bool
	Choice   string
}

// PromptHandler 由外部任务层实现。实现必须阻塞到用户回答或上下文取消。
type PromptHandler func(ctx context.Context, request PromptRequest) (PromptAnswer, error)

// ProgressObserver 是网络路径上的轻量回调集合。实现不得阻塞、不得反向调用
// Croc 的阻塞接口，也不得长时间持有锁；高频字节回调只应更新计数器。
type ProgressObserver struct {
	LocalRelay func(port string)
	// Peer报告对端元信息中的地址；地址不代表身份认证。
	Peer func(address string)
	// Phase 在真实阶段变化时调用，值使用库文档定义的阶段名。
	Phase func(phase string)
	// Files 在发送文件扫描完成或接收端拿到完整清单后调用。
	Files func(files []FileInfo, totalFolders int)
	// FileStart 在某个文件开始传输时调用。
	FileStart func(index int)
	// FileBytes 报告真实字节增量；session 为 false 表示断点续传复用的旧数据，
	// 不得计入本次网络传输字节。
	FileBytes func(index int, delta int64, session bool)
	// FileSkipped 报告未传输的文件；reason 为 unchanged 或 skipped。
	FileSkipped func(index int, reason string)
	// FileRenamed 报告接收端因重名而改写的目标文件名。
	FileRenamed func(index int, newName string)
	// FileCreated only reports files newly created with O_EXCL, never an existing target.
	FileCreated func(index int, file *os.File)
	// Lifecycle 报告 Croc 内部握手/文件清单/请求/传输/成功状态变化。
	Lifecycle func(channelSecured, fileInfoTransferred, recipientRequested, fileTransferred, successful bool)
}

// terminalOutput 返回当前客户端应写入的终端输出。库模式统一丢弃，
// 避免 Qt GUI 进程向不存在的控制台写进度条或提示文本。
func (c *Client) terminalOutput() (io.Writer, bool) {
	if c.Options.NoTerminalIO {
		return io.Discard, false
	}
	return termui.Output(os.Stderr)
}

// libraryPhase 根据 Croc 真实生命周期推算阶段。文件传输/校验的细化阶段由
// 任务层根据真实字节和校验结果继续推进。
func (c *Client) libraryPhase() string {
	state := c.lifecycleSnapshot()
	switch {
	case state.RecipientRequested || state.FileTransferred:
		return "transferring"
	case state.FileInfoTransferred:
		return "waiting_peer"
	case state.ChannelSecured:
		return "negotiating"
	default:
		return "connecting"
	}
}

func (c *Client) notifyPhase(phase string) {
	if c.Options.Progress.Phase != nil {
		c.Options.Progress.Phase(phase)
	}
}

func (c *Client) notifyLifecycle(state transferLifecycle) {
	if c.Options.Progress.Lifecycle != nil {
		c.Options.Progress.Lifecycle(
			state.ChannelSecured,
			state.FileInfoTransferred,
			state.RecipientRequested,
			state.FileTransferred,
			state.Successful,
		)
	}
}

func (c *Client) notifyFiles(files []FileInfo, totalFolders int) {
	if c.Options.Progress.Files != nil {
		c.Options.Progress.Files(files, totalFolders)
	}
}

func (c *Client) notifyFileStart(index int) {
	if c.Options.Progress.FileStart != nil {
		c.Options.Progress.FileStart(index)
	}
}

func (c *Client) notifyFileBytes(index int, delta int64, session bool) {
	if delta <= 0 || c.Options.Progress.FileBytes == nil {
		return
	}
	c.Options.Progress.FileBytes(index, delta, session)
}

func (c *Client) notifyFileSkipped(index int, reason string) {
	if c.Options.Progress.FileSkipped != nil {
		c.Options.Progress.FileSkipped(index, reason)
	}
}

func (c *Client) notifyFileRenamed(index int, newName string) {
	if c.Options.Progress.FileRenamed != nil {
		c.Options.Progress.FileRenamed(index, newName)
	}
}

// readPromptChoice 统一库模式与 CLI 的交互入口。库模式通过 PromptHandler
// 阻塞等待 C++ 回答；CLI 模式继续使用 stdin。
func (c *Client) readPromptChoice(ctx context.Context, request PromptRequest) (string, error) {
	if c.Options.PromptHandler == nil {
		return utils.GetInputContext(ctx, "")
	}
	c.notifyPhase("awaiting_confirmation")
	answer, err := c.Options.PromptHandler(ctx, request)
	if err != nil {
		return "", err
	}
	// 回答后回到真实生命周期阶段；后续字节/状态变化会继续推进。
	c.notifyPhase(c.libraryPhase())
	if answer.Accepted {
		return "y", nil
	}
	return "n", nil
}

// acceptReceivePrompt 把接收确认需要的元信息整理为结构化请求。
func acceptReceivePrompt(fileName string, fileCount int, totalBytes int64, peerID string) PromptRequest {
	return PromptRequest{
		Kind:       PromptReceiveAccept,
		FileName:   fileName,
		FileCount:  fileCount,
		TotalBytes: totalBytes,
		PeerID:     peerID,
		DefaultYes: true,
	}
}

// overwritePrompt 把覆盖/续传确认需要的元信息整理为结构化请求。
func overwritePrompt(index int, destination string, fileBytes int64, resumePercent float64) PromptRequest {
	name := path.Base(strings.ReplaceAll(destination, "\\", "/"))
	return PromptRequest{
		Kind:          PromptOverwrite,
		FileIndex:     index,
		FileName:      name,
		Destination:   destination,
		FileBytes:     fileBytes,
		ResumePercent: resumePercent,
		DefaultYes:    false,
	}
}

// LibraryFiles 返回当前接收/发送清单副本。调用方不得修改返回值影响引擎。
func (c *Client) LibraryFiles() []FileInfo {
	if c == nil {
		return nil
	}
	files := make([]FileInfo, len(c.FilesToTransfer))
	copy(files, c.FilesToTransfer)
	return files
}

// LibraryHashAlgorithm 返回当前协商后的哈希算法。
func (c *Client) LibraryHashAlgorithm() string {
	if c == nil {
		return ""
	}
	return c.Options.HashAlgorithm
}

// LibraryDeclinedFiles 返回被用户拒绝或跳过的文件数量。
func (c *Client) LibraryDeclinedFiles() int {
	if c == nil {
		return 0
	}
	count := 0
	for _, file := range c.FilesToTransfer {
		if file.declined {
			count++
		}
	}
	return count
}

// LibraryBundleUsed 表示本次传输是否实际使用 bundle 通道。
func (c *Client) LibraryBundleUsed() bool {
	if c == nil {
		return false
	}
	return c.bundleState().lifecycle.LastID > 0
}

// LibrarySuccessful 返回 Croc 内部记录的传输成功标志（最终以调用方错误为准）。
func (c *Client) LibrarySuccessful() bool {
	if c == nil {
		return false
	}
	return c.lifecycleSnapshot().Successful
}

// receiveFolderIsEmpty 在接收根目录内检查目录是否为空，避免依赖进程当前目录。
func (c *Client) receiveFolderIsEmpty(folder string) (bool, error) {
	root, err := c.receiveFilesystem()
	if err != nil {
		return false, err
	}
	dir, err := root.Open(folder)
	if err != nil {
		return false, err
	}
	defer dir.Close()
	names, err := dir.Readdirnames(1)
	if err == io.EOF {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return len(names) == 0, nil
}

// 同进程的库任务通过内存定位自己的本地中继；不改发现包和Croc协议。
// 口令只作内存键，不写日志/磁盘；取消时仅删除自己登记的对象。
var libraryLocalRelays sync.Map
func (c *Client) registerLibraryLocalRelay() {
    if !c.Options.NoTerminalIO || !c.Options.OnlyLocal { return }
    libraryLocalRelays.Store(c.Options.SharedSecret, c)
    if c.Options.Progress.LocalRelay != nil { c.Options.Progress.LocalRelay(c.localRelayPort) }
    context.AfterFunc(c.stop.ctx, func() { libraryLocalRelays.CompareAndDelete(c.Options.SharedSecret, c) })
}
func (c *Client) sameProcessLibraryRelay() string {
    if !c.Options.NoTerminalIO || !c.Options.OnlyLocal { return "" }
    if value, ok := libraryLocalRelays.Load(c.Options.SharedSecret); ok {
        sender := value.(*Client)
        if sender.stop.ctx.Err() == nil { return "127.0.0.1:" + sender.localRelayPort }
    }
    return ""
}
