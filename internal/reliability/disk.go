package reliability

import (
	"io/fs"
	"path/filepath"

	"agent/internal/config"
)

type DiskStatus string

const (
	DiskOK      DiskStatus = "OK"
	DiskSoft    DiskStatus = "SOFT_PRESSURE"
	DiskHard    DiskStatus = "HARD_PRESSURE"
	DiskUnknown DiskStatus = "UNKNOWN"
)

type DiskReport struct {
	Status     DiskStatus       `json:"status"`
	TotalBytes int64            `json:"total_bytes"`
	Paths      map[string]int64 `json:"paths"`
}

func CheckDiskPressure(cfg config.DiskPressureConfig) DiskReport {
	report := DiskReport{Status: DiskOK, Paths: map[string]int64{}}
	for _, path := range cfg.Paths {
		size, err := directorySize(path)
		if err != nil {
			report.Status = DiskUnknown
			report.Paths[path] = 0
			continue
		}
		report.Paths[path] = size
		report.TotalBytes += size
	}
	if cfg.HardLimitBytes > 0 && report.TotalBytes >= cfg.HardLimitBytes {
		report.Status = DiskHard
	} else if cfg.SoftLimitBytes > 0 && report.TotalBytes >= cfg.SoftLimitBytes && report.Status == DiskOK {
		report.Status = DiskSoft
	}
	return report
}

func directorySize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}
