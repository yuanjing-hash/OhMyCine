package logging

import (
	"fmt"
	"strings"
)

const MiB int64 = 1024 * 1024

type Policy struct {
	Level         string `json:"level"`
	MaxFileMiB    int    `json:"max_file_mib"`
	MaxBackups    int    `json:"max_backups"`
	RetentionDays int    `json:"retention_days"`
	MaxTotalMiB   int    `json:"max_total_mib"`
}

func DefaultPolicy(environment string) Policy {
	level := "info"
	if strings.ToLower(environment) != "production" {
		level = "debug"
	}
	return Policy{Level: level, MaxFileMiB: 20, MaxBackups: 10, RetentionDays: 30, MaxTotalMiB: 500}
}

func (p Policy) Validate() error {
	if p.Level != "debug" && p.Level != "info" && p.Level != "warn" && p.Level != "error" {
		return fmt.Errorf("level must be debug, info, warn, or error")
	}
	if p.MaxFileMiB < 1 || p.MaxFileMiB > 256 {
		return fmt.Errorf("max_file_mib must be between 1 and 256")
	}
	if p.MaxBackups < 1 || p.MaxBackups > 100 {
		return fmt.Errorf("max_backups must be between 1 and 100")
	}
	if p.RetentionDays < 1 || p.RetentionDays > 365 {
		return fmt.Errorf("retention_days must be between 1 and 365")
	}
	if p.MaxTotalMiB < 32 || p.MaxTotalMiB > 10240 {
		return fmt.Errorf("max_total_mib must be between 32 and 10240")
	}
	if p.MaxTotalMiB < p.MaxFileMiB {
		return fmt.Errorf("max_total_mib must not be smaller than max_file_mib")
	}
	return nil
}
