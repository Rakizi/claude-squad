package log

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

var (
	WarningLog *log.Logger
	InfoLog    *log.Logger
	ErrorLog   *log.Logger
)

var logFileName = filepath.Join(os.TempDir(), "claudesquad.log")

var globalLogFile *os.File

// Initialize should be called once at the beginning of the program to set up logging.
// defer Close() after calling this function. It sets the go log output to the file in
// the os temp directory.

func Initialize(daemon bool) {
	f, err := os.OpenFile(logFileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		panic(fmt.Sprintf("could not open log file: %s", err))
	}

	// Set log format to include timestamp and file/line number
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	fmtS := "%s"
	if daemon {
		fmtS = "[DAEMON] %s"
	}
	InfoLog = log.New(f, fmt.Sprintf(fmtS, "INFO:"), log.Ldate|log.Ltime|log.Lshortfile)
	WarningLog = log.New(f, fmt.Sprintf(fmtS, "WARNING:"), log.Ldate|log.Ltime|log.Lshortfile)
	ErrorLog = log.New(f, fmt.Sprintf(fmtS, "ERROR:"), log.Ldate|log.Ltime|log.Lshortfile)

	globalLogFile = f
}

// Infof and Warningf log without assuming Initialize has run.
//
// ⛔ InfoLog, WarningLog and ErrorLog are nil until Initialize sets them, and a
// nil *log.Logger PANICS on Printf. The interface initialises logging at
// startup; a test, a library caller or a CLI subcommand that forgot may not --
// which is the nil-pointer segfault mid-teardown that kill.go's header records.
//
// A caller that must log should never have to know whether logging is up. These
// drop the line when it is not, because a missing log line is not a reason to
// take the program down.
func Infof(format string, args ...any) {
	if InfoLog != nil {
		InfoLog.Printf(format, args...)
	}
}

func Warningf(format string, args ...any) {
	if WarningLog != nil {
		WarningLog.Printf(format, args...)
	}
}

func Close() {
	_ = globalLogFile.Close()
	// TODO: maybe only print if verbose flag is set?
	fmt.Println("wrote logs to " + logFileName)
}

// Every is used to log at most once every timeout duration.
type Every struct {
	timeout time.Duration
	timer   *time.Timer
}

func NewEvery(timeout time.Duration) *Every {
	return &Every{timeout: timeout}
}

// ShouldLog returns true if the timeout has passed since the last log.
func (e *Every) ShouldLog() bool {
	if e.timer == nil {
		e.timer = time.NewTimer(e.timeout)
		e.timer.Reset(e.timeout)
		return true
	}

	select {
	case <-e.timer.C:
		e.timer.Reset(e.timeout)
		return true
	default:
		return false
	}
}
