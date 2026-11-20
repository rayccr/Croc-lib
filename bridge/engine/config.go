package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/schollz/croc/v11/src/codephrase"
	"github.com/schollz/croc/v11/src/croc"
	"github.com/schollz/croc/v11/src/models"
	"github.com/schollz/pake/v3"
)

var throttlePattern = regexp.MustCompile(`^[0-9]+[kKmMgG]?$`)

// sendConfig 是 Croc_StartSend 的严格 JSON 配置。未知字段会被拒绝，避免
// 把未实现的选项静默忽略。
type sendConfig struct {
	Code             string   `json:"code"`
	Files            []string `json:"files"`
	ZipFolder        bool     `json:"zip_folder"`
	Relay            string   `json:"relay"`
	Relay6           string   `json:"relay6"`
	RelayPassword    string   `json:"relay_password"`
	OnlyLocal        bool     `json:"only_local"`
	DisableLocal     *bool    `json:"disable_local"`
	LocalRelayPorts  []string `json:"local_relay_ports"`
	NoCompress       bool     `json:"no_compress"`
	NoMultiplexing   bool     `json:"no_multiplexing"`
	HashAlgorithm    string   `json:"hash_algorithm"`
	Curve            string   `json:"curve"`
	ThrottleUpload   string   `json:"throttle_upload"`
	Transport        string   `json:"transport"`
	MulticastAddress string   `json:"multicast_address"`
	Exclude          []string `json:"exclude"`
	ExcludeFile      []string `json:"exclude_file"`
	Ask              bool     `json:"ask"`
	PromptTimeoutSec int      `json:"prompt_timeout_seconds"`
}

// receiveConfig 是 Croc_StartReceive 的严格 JSON 配置。
type receiveConfig struct {
	Code             string   `json:"code"`
	SavePath         string   `json:"save_path"`
	AutoAccept       *bool    `json:"auto_accept"`
	Overwrite        bool     `json:"overwrite"`
	Rename           bool     `json:"rename"`
	Relay            string   `json:"relay"`
	Relay6           string   `json:"relay6"`
	RelayPassword    string   `json:"relay_password"`
	OnlyLocal        bool     `json:"only_local"`
	DisableLocal     *bool    `json:"disable_local"`
	LocalRelayPorts  []string `json:"local_relay_ports"`
	NoCompress       bool     `json:"no_compress"`
	NoMultiplexing   bool     `json:"no_multiplexing"`
	Curve            string   `json:"curve"`
	Transport        string   `json:"transport"`
	MulticastAddress string   `json:"multicast_address"`
	IP               string   `json:"ip"`
	PromptTimeoutSec int      `json:"prompt_timeout_seconds"`
}

func decodeStrict(data []byte, value any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON data")
		}
		return err
	}
	return nil
}

func parseSendConfig(data []byte) (sendConfig, croc.Options, error) {
	var cfg sendConfig
	if err := decodeStrict(data, &cfg); err != nil {
		return cfg, croc.Options{}, fmt.Errorf("invalid send config JSON: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return cfg, croc.Options{}, err
	}
	options, err := cfg.buildOptions()
	if err != nil {
		return cfg, croc.Options{}, err
	}
	return cfg, options, nil
}

func parseReceiveConfig(data []byte) (receiveConfig, croc.Options, error) {
	var cfg receiveConfig
	if err := decodeStrict(data, &cfg); err != nil {
		return cfg, croc.Options{}, fmt.Errorf("invalid receive config JSON: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return cfg, croc.Options{}, err
	}
	options, err := cfg.buildOptions()
	if err != nil {
		return cfg, croc.Options{}, err
	}
	return cfg, options, nil
}

func (c *sendConfig) validate() error {
	if err := validateCode(c.Code); err != nil {
		return err
	}
	if len(c.Files) == 0 {
		return errors.New("files must contain at least one path")
	}
	for index, name := range c.Files {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("files[%d] is empty", index)
		}
		// The scanner only expands patterns with '*'; '[' alone is a literal
		// filename character, including on Windows.
		if strings.ContainsAny(name, "*?") {
			return fmt.Errorf("files[%d] contains a wildcard; expand it before calling the engine", index)
		}
		if !filepath.IsAbs(name) {
			return fmt.Errorf("files[%d] must be an absolute path: %q", index, name)
		}
		if _, err := os.Lstat(name); err != nil {
			return fmt.Errorf("files[%d] is not accessible: %w", index, err)
		}
	}
	if err := validateNetworkOptions(
		c.Relay, c.Relay6, c.RelayPassword, c.OnlyLocal, c.DisableLocal,
		c.LocalRelayPorts, c.Transport, c.Curve,
	); err != nil {
		return err
	}
	if c.HashAlgorithm != "" && !isSupportedHash(c.HashAlgorithm) {
		return fmt.Errorf("unsupported hash_algorithm %q", c.HashAlgorithm)
	}
	if c.ThrottleUpload != "" {
		if !throttlePattern.MatchString(c.ThrottleUpload) {
			return fmt.Errorf("invalid throttle_upload %q", c.ThrottleUpload)
		}
		digits := strings.TrimRight(c.ThrottleUpload, "kKmMgG")
		if value, err := strconv.ParseInt(digits, 10, 64); err != nil || value <= 0 {
			return fmt.Errorf("invalid throttle_upload %q", c.ThrottleUpload)
		}
	}
	if c.PromptTimeoutSec < 0 || c.PromptTimeoutSec > 3600 {
		return errors.New("prompt_timeout_seconds must be between 0 and 3600")
	}
	return nil
}

func (c *receiveConfig) validate() error {
    if c.OnlyLocal && c.IP != "" {
        endpoint, err := netip.ParseAddrPort(c.IP)
        if err != nil || endpoint.Port() == 0 || !(endpoint.Addr().IsPrivate() || endpoint.Addr().IsLoopback() || endpoint.Addr().IsLinkLocalUnicast()) { return errors.New("only_local ip must be a private, link-local or loopback literal IP:port") }
    }
	if err := validateCode(c.Code); err != nil {
		return err
	}
	if strings.TrimSpace(c.SavePath) == "" {
		return errors.New("save_path is required")
	}
	if !filepath.IsAbs(c.SavePath) {
		return fmt.Errorf("save_path must be an absolute path: %q", c.SavePath)
	}
	info, err := os.Stat(c.SavePath)
	if err != nil {
		return fmt.Errorf("save_path is not accessible: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("save_path is not a directory: %q", c.SavePath)
	}
	probe, err := os.CreateTemp(c.SavePath, ".croc-engine-write-*")
	if err != nil {
		return fmt.Errorf("save_path is not writable: %w", err)
	}
	probeName := probe.Name()
	closeErr := probe.Close()
	removeErr := os.Remove(probeName)
	if closeErr != nil {
		return fmt.Errorf("close save_path probe: %w", closeErr)
	}
	if removeErr != nil {
		return fmt.Errorf("remove save_path probe: %w", removeErr)
	}
	if err := validateNetworkOptions(
		c.Relay, c.Relay6, c.RelayPassword, c.OnlyLocal, c.DisableLocal,
		c.LocalRelayPorts, c.Transport, c.Curve,
	); err != nil {
		return err
	}
	if c.Transport != "" && strings.ToLower(c.Transport) != string(croc.TransportAuto) {
		return errors.New("receiver transport must be auto; sender selects the data transport")
	}
	if c.PromptTimeoutSec < 0 || c.PromptTimeoutSec > 3600 {
		return errors.New("prompt_timeout_seconds must be between 0 and 3600")
	}
	return nil
}

func validateCode(code string) error {
	if strings.TrimSpace(code) != code {
		return errors.New("code must not contain leading or trailing whitespace")
	}
	if _, err := codephrase.Parse(code); err != nil {
		return fmt.Errorf("invalid code: %w", err)
	}
	return nil
}

func isSupportedHash(value string) bool {
	switch strings.ToLower(value) {
	case "xxhash", "md5", "highway", "imohash":
		return true
	default:
		return false
	}
}

func validateCurve(curve string) error {
	if curve == "" {
		return nil
	}
	for _, available := range pake.AvailableCurves() {
		if curve == available {
			return nil
		}
	}
	return fmt.Errorf("unsupported curve %q", curve)
}

func validateNetworkOptions(
	relay, relay6, password string,
	onlyLocal bool,
	disableLocal *bool,
	localPorts []string,
	transport, curve string,
) error {
	if err := validateCurve(curve); err != nil {
		return err
	}
	effectiveDisableLocal := true
	if disableLocal != nil {
		effectiveDisableLocal = *disableLocal
	}
	if onlyLocal {
		effectiveDisableLocal = false
	}
	if !effectiveDisableLocal {
		if len(localPorts) < 2 {
			return errors.New("local_relay_ports must contain at least two ports when disable_local is false")
		}
		seen := make(map[string]bool, len(localPorts))
		for _, port := range localPorts {
			number, err := strconv.Atoi(port)
			if err != nil || number < 1 || number > 65535 {
				return fmt.Errorf("invalid local relay port %q", port)
			}
			if seen[port] {
				return fmt.Errorf("duplicate local relay port %q", port)
			}
			seen[port] = true
		}
	} else if len(localPorts) != 0 {
		return errors.New("local_relay_ports requires disable_local to be false")
	}
	if onlyLocal && transport != "" && strings.ToLower(transport) != string(croc.TransportAuto) {
		return errors.New("only_local requires transport auto")
	}
	if _, err := croc.ParseTransportMode(transport); err != nil {
		return err
	}
	_ = relay
	_ = relay6
	_ = password
	return nil
}

func (c *sendConfig) buildOptions() (croc.Options, error) {
	relay, relay6, password, disableLocal := networkDefaults(c.Relay, c.Relay6, c.RelayPassword, c.OnlyLocal, c.DisableLocal)
	transport, err := croc.ParseTransportMode(c.Transport)
	if err != nil {
		return croc.Options{}, err
	}
	hashAlgorithm := strings.ToLower(c.HashAlgorithm)
	if hashAlgorithm == "" {
		hashAlgorithm = "xxhash"
	}
	curve := c.Curve
	if curve == "" {
		curve = "p256"
	}
	multicast := c.MulticastAddress
	if multicast == "" {
		multicast = "239.255.255.250"
	}
	options := croc.Options{
		IsSender:          true,
		SharedSecret:      c.Code,
		RelayAddress:      relay,
		RelayAddress6:     relay6,
		RelayPassword:     password,
		RelayPorts:        append([]string(nil), c.LocalRelayPorts...),
		NoPrompt:          true,
		NoMultiplexing:    c.NoMultiplexing,
		DisableLocal:      disableLocal,
		OnlyLocal:         c.OnlyLocal,
		Ask:               c.Ask,
		NoCompress:        c.NoCompress,
		Curve:             curve,
		HashAlgorithm:     hashAlgorithm,
		ThrottleUpload:    c.ThrottleUpload,
		ZipFolder:         c.ZipFolder,
		MulticastAddress:  multicast,
		Exclude:           append([]string(nil), c.Exclude...),
		ExcludeFile:       append([]string(nil), c.ExcludeFile...),
		Transport:         transport,
		DisableClipboard:  true,
		NoTerminalIO:      true,
		NoBundle:          true,
		NoStagedTransport: true,
		PromptHandler:     nil,                     // 任务创建时绑定
		Progress:          croc.ProgressObserver{}, // 任务创建时绑定
	}
	return options, nil
}

func (c *receiveConfig) buildOptions() (croc.Options, error) {
	relay, relay6, password, disableLocal := networkDefaults(c.Relay, c.Relay6, c.RelayPassword, c.OnlyLocal, c.DisableLocal)
	curve := c.Curve
	if curve == "" {
		curve = "p256"
	}
	multicast := c.MulticastAddress
	if multicast == "" {
		multicast = "239.255.255.250"
	}
	autoAccept := true
	if c.AutoAccept != nil {
		autoAccept = *c.AutoAccept
	}
	options := croc.Options{
		IsSender:          false,
		SharedSecret:      c.Code,
		RelayAddress:      relay,
		RelayAddress6:     relay6,
		RelayPassword:     password,
		RelayPorts:        append([]string(nil), c.LocalRelayPorts...),
		NoPrompt:          autoAccept,
		NoMultiplexing:    c.NoMultiplexing,
		DisableLocal:      disableLocal,
		OnlyLocal:         c.OnlyLocal,
		NoCompress:        c.NoCompress,
		Overwrite:         c.Overwrite,
		Rename:            c.Rename,
		Curve:             curve,
		MulticastAddress:  multicast,
		IP:                c.IP,
		Transport:         croc.TransportAuto,
		DisableClipboard:  true,
		NoTerminalIO:      true,
		NoBundle:          true,
		NoStagedTransport: true,
		ReceiveFolder:     c.SavePath,
		PromptHandler:     nil,
		Progress:          croc.ProgressObserver{},
	}
	return options, nil
}

func networkDefaults(
	relay, relay6, password string,
	onlyLocal bool,
	disableLocal *bool,
) (string, string, string, bool) {
	if onlyLocal {
		return "", "", passwordOrDefault(password), false
	}
	if strings.TrimSpace(relay) == "" {
		relay = models.DEFAULT_RELAY
	}
	if strings.TrimSpace(relay6) == "" {
		if relay == models.DEFAULT_RELAY {
			relay6 = models.DEFAULT_RELAY6
		}
	}
	if relay != models.DEFAULT_RELAY {
		relay6 = ""
	}
	effectiveDisableLocal := true
	if disableLocal != nil {
		effectiveDisableLocal = *disableLocal
	}
	return relay, relay6, passwordOrDefault(password), effectiveDisableLocal
}

func passwordOrDefault(password string) string {
	if password == "" {
		return models.DEFAULT_PASSPHRASE
	}
	return password
}
