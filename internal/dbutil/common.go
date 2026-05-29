package dbutil

const (
	TimeFormat   = "2006-01-02 15:04:05"
	DriverName   = "sqlite"
	PragmaWAL    = "?_pragma=journal_mode(WAL)"
	PragmaFK     = "&_pragma=foreign_keys(1)"
	PragmaBusy5s = "&_pragma=busy_timeout(5000)"
)
