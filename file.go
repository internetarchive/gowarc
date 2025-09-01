package warc

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// generateWARCFilename generate a WARC file name following recommendations of the specs:
// Prefix-Timestamp-Serial-Crawlhost.warc.gz
func generateWARCFilename(prefix string, compression string, serial *atomic.Uint64) string {
	// Get host name as reported by the kernel
	var filename strings.Builder

	filename.WriteString(prefix)
	filename.WriteString("-")

	now := time.Now().UTC()
	filename.WriteString(now.Format("20060102150405") + strconv.Itoa(now.Nanosecond())[:3])
	filename.WriteString("-")

	var newSerial uint64
	for {
		oldSerial := serial.Load()
		if oldSerial >= 99999 {
			if serial.CompareAndSwap(oldSerial, 1) {
				newSerial = 1
				break
			}
		} else {
			if serial.CompareAndSwap(oldSerial, oldSerial+1) {
				newSerial = oldSerial + 1
				break
			}
		}
	}
	filename.WriteString(formatSerial(newSerial, "5"))
	filename.WriteString("-")

	hostName, err := os.Hostname()
	if err != nil {
		panic(err)
	}
	filename.WriteString(hostName)

	var fileExt string
	switch strings.ToLower(compression) {
	case "gzip":
		fileExt = ".warc.gz.open"
	case "zstd":
		fileExt = ".warc.zst.open"
	default:
		fileExt = ".warc.open"
	}

	filename.WriteString(fileExt)

	return filename.String()
}

// formatSerial add the correct padding to the serial
// E.g. with serial = 23 and format = 5:
// formatSerial return 00023
func formatSerial(serial uint64, format string) string {
	return fmt.Sprintf("%0"+format+"d", serial)
}

// isFielSizeExceeded compare the size of a file (filePath) with
// a max size (maxSize), if the size of filePath exceed maxSize,
// it returns true, else, it returns false
func isFileSizeExceeded(file *os.File, maxSize float64) bool {
	// Get actual file size
	stat, err := file.Stat()
	if err != nil {
		panic(err)
	}
	fileSize := (float64)((stat.Size() / 1024) / 1024)

	// If fileSize exceed maxSize, return true
	return fileSize >= maxSize
}
