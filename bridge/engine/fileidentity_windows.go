package engine

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// createdFileIdentity identifies the open file rather than trusting its name.
func createdFileIdentity(file *os.File) string {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d:%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh,
		info.FileIndexLow, info.CreationTime.HighDateTime, info.CreationTime.LowDateTime)
}
