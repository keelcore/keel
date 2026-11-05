package config

import "fmt"

// validateLogRotation rejects negative limits; zero turns a limit off.
func validateLogRotation(r LogRotationConfig) error {
	if r.MaxSizeMB < 0 {
		return fmt.Errorf("logging.rotation.max_size_mb must not be negative; got %d", r.MaxSizeMB)
	}
	if r.MaxAgeDays < 0 {
		return fmt.Errorf("logging.rotation.max_age_days must not be negative; got %d", r.MaxAgeDays)
	}
	if r.MaxBackups < 0 {
		return fmt.Errorf("logging.rotation.max_backups must not be negative; got %d", r.MaxBackups)
	}
	return nil
}

// validateAudit: an enabled audit stream needs somewhere to write.
func validateAudit(a AuditConfig) error {
	if err := validateLogRotation(a.Rotation); err != nil {
		return err
	}
	if a.Enabled && a.File == "" && !(a.RemoteSink.Enabled && a.RemoteSink.Endpoint != "") {
		return fmt.Errorf("audit.enabled needs audit.file or audit.remote_sink")
	}
	return nil
}
