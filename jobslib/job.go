package jobslib

// JobState is a type which indicates the completion state of a Linux process
// associated with a job.
type JobState uint8

const (
	// Unspecified says that the process state is unknown.
	Unspecified JobState = iota

	// Running indicates that the process is currently running/executing.
	Running

	// TerminatedByAPI indicates that the process was terminated by the
	// jobmaster API.
	TerminatedByAPI

	// Completed indicates that the job either completed naturally, or was
	// terminated by the Linux system (perhaps due to overrunning cgroup quotas).
	Completed
)

func (s JobState) String() string {
	return [...]string{"Unspecified", "Running", "TerminatedByAPI", "Completed"}[s]
}

// Job is a struct which holds information about a job in the jobmaster system.
type Job struct {
	// ID is the job identifier.
	ID string

	// Mesg is an informational message. It does not have anything to do with the job output.
	Mesg string

	// ClientName is the name of the client/user who started the job.
	ClientName string

	// PID is the process ID of the job. It can go up to 2^22, although in practice the kernel
	// usually sets a max of 32,768 before they wrap around.
	PID int32

	// State is the execution status of the job - whether still running, or terminated.
	State JobState

	// ExitCode is the exit code for a job which is no longer running. A value of -1 in this
	// field indicates that the job was terminated by a signal.
	ExitCode int

	// SignalStr contains the string representation of the signal which terminated the job.
	// Jobs which completed naturally - i.e without being forcibly terminated - will have this
	// field be empty.
	SignalStr string

	// Command is the Linux command which was started by this job.
	Command string

	// CmdArgs is the optional list of command-line arguments to Command.
	CmdArgs []string

	// StartedUnixMilli is the timestamp, as milliseconds since the Unix Epoch, of the time
	// at which the job was started.
	StartedUnixMilli int64

	// EndedUnixMilli is the timestamp, as milliseconds since the Unix Epoch, of the time at
	// which the job ended or was terminated.
	EndedUnixMilli int64

	// OutputFilePath is the fully-qualified path to the file which will contain the output
	// from the Linux process associated with this job.
	OutputFilePath string
}
