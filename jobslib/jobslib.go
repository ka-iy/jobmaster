package jobslib

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/segmentio/ksuid"
)

// TODO: Better error handling, with error types, so downstream callers can use errors.Is() and return
// appropriate errors to _their_ callers.

// JobsLib is the concretization of the job library.
type JobsLib struct {

	// jobs is a map of jobID to a job.
	jobs map[string]*Job

	// clientToJobID is a map from a client/user ID to a list of job IDs.
	// This map is only written to once - when a job is initially created.
	clientToJobID map[string][]string

	// mu is a Read-Write mutex protecting access to the above data structures.
	mu sync.RWMutex

	// clientList is the list of clients passed in when creating JobsLib.
	clientList []string

	// outputDir is the path to a directory where the job output files will be written.
	// If this is not provided at creation time, it defaults to "/tmp".
	outputDir string

	// ioDevMajorMinor is the device number of the IO block device to use for setting
	// the per-job IO limit/quota. IO limits for all jobs will be set for this block
	// device. TODO: Support setting different device numbers per job.
	ioDevMajorMinor string

	// watcherLock is a mutex taken by the filesystem watcher/notifier method at the start
	// of watching events for the output file of a running job. The watcher method will
	// release this lock when it is signaled that the job has terminated. Readers will
	// use this in conjunction with CVEvent to determine that they no longer need to
	// wait for further new events to the file from which they are reading.
	WatcherLock sync.Mutex

	// CvEvent is a condition variable used by the filesystem watcher/notifier to signal
	// readers of the output/log files of uncompleted processes that there are new events
	// for the file from which they are reading/streaming.
	// Channels cannot be used for this signaling for a couple of reasons:
	// (1) The number of readers is non-deterministic and not in the job library's control;
	// (2) When a message is sent to a channel, which reader/consumer picks it up is non-
	//  deterministic in the case that there are multiple readers/consumers;
	// (3) For more than one reader, reading from a channel would consume the file
	//  event, leaving it lost to other readers unless complicated measures were taken.
	CvEvent *sync.Cond

	JobState
}

// validateIODeviceNumber checks whether ioDevMajorMinor is in the expected format for a
// valid Linux IO block device identifier.
// A valid device ID should start with 1 to 3 digits, followed by ":", followed by 1 to 3 digits.
// See https://www.kernel.org/doc/Documentation/admin-guide/devices.txt
func validateIODeviceNumber(ioDevMajorMinor string) bool {
	var validDevID = regexp.MustCompile(`^[0-9]{1,3}:[0-9]{1,3}$`)
	return validDevID.MatchString(ioDevMajorMinor)
}

// generateKsuidAsString is a convenience function to get a ksuid in string form.
// This is used as a job ID.
func (j *JobsLib) generateKsuidAsString() (string, error) {
	ks, err := ksuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("failed generating KSUID: %w", err)
	}
	return ks.String(), nil
}

// findJobByJobID returns the job having jobID as its ID.
func (j *JobsLib) findJobByJobID(ctx context.Context, jobID string) (*Job, error) {
	if jobID == "" {
		return nil, errors.New("cannot findJobByJobID, jobID is empty")
	}

	// Before we do any work, check that the context wasn't already canceled.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Take a read lock before we go hunting
	j.mu.RLock()
	defer j.mu.RUnlock()
	theJob := j.jobs[jobID]

	if theJob == nil {
		return nil, fmt.Errorf("job with ID %q was not found", jobID)
	}

	return theJob, nil
}

// deepCopyJobFromPointer returns a Job whose data members, including the string members,
// have been defensively deep-copied from pJob.
func (j *JobsLib) deepCopyJobFromPointer(ctx context.Context, pJob *Job) (Job, error) {
	retval := Job{}
	if pJob == nil {
		return retval, errors.New("source job pointer is nil")
	}

	// Are we done?
	if ctx.Err() != nil {
		return retval, ctx.Err()
	}

	// Take a read lock for safety
	j.mu.RLock()
	defer j.mu.RUnlock()

	// deep-copy the string slice
	cCmdArgs := make([]string, len(pJob.CmdArgs))
	_ = copy(cCmdArgs, pJob.CmdArgs)

	// Copy the other things.
	retval.ID = strings.Clone(pJob.ID)
	retval.Mesg = strings.Clone(pJob.Mesg)
	retval.ClientName = strings.Clone(pJob.ClientName)
	retval.PID = pJob.PID
	retval.State = pJob.State
	retval.ExitCode = pJob.ExitCode
	retval.SignalStr = strings.Clone(pJob.SignalStr)
	retval.Command = strings.Clone(pJob.Command)
	retval.CmdArgs = cCmdArgs
	retval.OutputFilePath = strings.Clone(pJob.OutputFilePath)
	retval.StartedUnixMilli = pJob.StartedUnixMilli
	retval.EndedUnixMilli = pJob.EndedUnixMilli

	return retval, nil
}

// findAndCopyJobByID looks for a job having the given jobID, and returns a copy of it.
func (j *JobsLib) findAndCopyJobByID(ctx context.Context, jobID string) (Job, error) {
	pJob, err := j.findJobByJobID(ctx, jobID)
	if err != nil {
		return Job{}, err
	}

	return j.deepCopyJobFromPointer(ctx, pJob)
}

// CreateJobsLib creates a JobsLib instance, using the supplied clientList.
// It sets up the cgroup-v2 hierarchy required for setting per-job CPU, IO, and memory limits.
// Job output from started jobs will be stored as files under outputDir.
// ioDevMajorMinor is required for setting up the IO limit/quota for started jobs, and
// is a string of the form "<Major>:<Minor>", for example "254:0".
func CreateJobsLib(clientList []string, outputDir, ioDevMajorMinor string) (*JobsLib, error) {
	if len(clientList) == 0 {
		return nil, errors.New("cannot create JobsLib: clientList is empty")
	}

	if outputDir == "" || strings.TrimSpace(outputDir) == "" {
		outputDir = "/tmp"
	}

	if !validateIODeviceNumber(ioDevMajorMinor) {
		return nil, fmt.Errorf("cannot create JobsLib: ioDevMajorMinor %q is not in a valid dev ID format", ioDevMajorMinor)
	}

	err := setupJobmasterCgroups(clientList)
	if err != nil {
		return nil, fmt.Errorf("cannot create JobsLib: failed cgroup setup due to %w", err)
	}

	return &JobsLib{
		jobs:            make(map[string]*Job),
		clientToJobID:   make(map[string][]string),
		clientList:      clientList,
		outputDir:       outputDir,
		ioDevMajorMinor: ioDevMajorMinor,
		CvEvent:         sync.NewCond(&sync.Mutex{}),
	}, nil
}

// commonSanity is an internal helper method to perform input arg sanity checks for all operations
// other than Start.
func (j *JobsLib) commonSanity(ctx context.Context, clientName, jobID string, overrideClientCheck bool) error {
	if !overrideClientCheck && clientName == "" {
		return errors.New("clientName is empty and we were not told to override client checks")
	}
	if jobID == "" {
		return errors.New("jobID is empty")
	}

	if ctx.Err() != nil {
		return fmt.Errorf("context reported error: %w", ctx.Err())
	}

	return nil
}

// Start starts the given job in a job-specific cgroup under clientName's cgroup directory.
// If successful, it returns the job ID with which the job was created.
func (j *JobsLib) Start(ctx context.Context, clientName, command string, cmdArgs []string) (string, error) {
	if clientName == "" {
		return "", errors.New("cannot Start, clientName is empty")
	}
	if command == "" {
		return "", errors.New("cannot Start, command is empty")
	}

	err := verifyClientCgroup(clientName)
	if err != nil {
		return "", fmt.Errorf("cannot Start, error verifying cgroup dir for %q: %w", clientName, err)
	}

	// Before we do any work, check that the context wasn't already canceled
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	jobID, err := j.generateKsuidAsString()
	if err != nil {
		return "", fmt.Errorf("cannot Start, failed generating job ID: %w", err)
	}

	logMsgPrefix := fmt.Sprintf("JobsLib Start: ClientName=%s JobID=%s: %s %v :", clientName, jobID, command, cmdArgs)

	log.Printf("%s Creating job cgroup", logMsgPrefix)
	jcgPath, err := createJobCgroup(jobID, clientName, j.ioDevMajorMinor)
	if err != nil {
		return "", fmt.Errorf("cannot Start, failed to create job cgroup: %w", err)
	}

	// Get a file descriptor handle to the job's cgroup path
	cgfd, err := syscall.Open(jcgPath, syscall.O_DIRECTORY, 0)
	if err != nil {
		return "", fmt.Errorf("cannot Start, failed to get fd handle to job cgroup dir %q: %w", jcgPath, err)
	}
	defer syscall.Close(cgfd)

	// Now let's create a file to hold the job's output
	jobOutputFileName := fmt.Sprintf("%s.log", jobID)
	jobOutputFilePath := filepath.Join(j.outputDir, jobOutputFileName)
	log.Printf("%s Creating job output file: %s", logMsgPrefix, jobOutputFilePath)
	joFile, err := os.Create(jobOutputFilePath)
	if err != nil {
		return "", fmt.Errorf("cannot Start, failed to create job output log file %q: %w", jobOutputFilePath, err)
	}

	// Set up the command execution with automatic startup in the job-specific cgroup
	cmd := exec.Command(command, cmdArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    cgfd,
	}
	// Redirect output to our log file
	cmd.Stdout = joFile
	cmd.Stderr = joFile

	// Start the process.
	log.Printf("%s Starting process with output file: %s", logMsgPrefix, jobOutputFilePath)
	startedUnixMilli := time.Now().UnixMilli()
	if err = cmd.Start(); err != nil {
		joFile.Close()
		rErr := os.Remove(jobOutputFilePath)
		if rErr != nil {
			return "", fmt.Errorf("cannot Start, command startup failed due to %w and removing the output file also errored with %w", err, rErr)
		}

		return "", fmt.Errorf("cannot Start, command startup failed: %w", err)
	}
	defer joFile.Close()

	// Now that we have the job running, let's create the job object
	job := &Job{
		ID:               jobID,
		ClientName:       clientName,
		PID:              int32(cmd.Process.Pid),
		State:            Running,
		Command:          command,
		CmdArgs:          cmdArgs,
		OutputFilePath:   jobOutputFilePath,
		StartedUnixMilli: startedUnixMilli,
	}

	log.Printf("%s Started process with PID=%d, state set to %s", logMsgPrefix, cmd.Process.Pid, job.State)

	// Update our data structures under a write lock.
	j.mu.Lock()
	defer j.mu.Unlock()
	j.jobs[jobID] = job
	j.clientToJobID[clientName] = append(j.clientToJobID[clientName], jobID)

	// Now thread off a goroutine to wait for job completion.
	go func() {
		// Thread off the watcher with a new context, which we will cancel when we are done.
		// A channel did not prove reliable here.
		wctx, wcancel := context.WithCancel(context.Background())

		go j.Watcher(wctx, jobOutputFilePath)

		var exitcode int
		sigStr := ""
		pmsg := cmd.ProcessState.String()
		if err := cmd.Wait(); err != nil {
			var exiterr *exec.ExitError
			if errors.As(err, &exiterr) {
				// exiterr is of type https://pkg.go.dev/syscall#WaitStatus
				exitcode = exiterr.ExitCode()
				if exitcode == -1 {
					// NOTE: -1 from ExitCode() means that either the process hasn't exited, or
					// the process was terminated by signal: https://pkg.go.dev/os#ProcessState.ExitCode
					// For getting to syscall.WaitStatus, see https://github.com/golang/go/issues/58988
					ws := exiterr.Sys().(syscall.WaitStatus)
					// Convert signal to string. We are only concerned with termination signals.
					sigStr = ws.Signal().String()
				}
			} else {
				pmsg = fmt.Sprintf("%s, %v", pmsg, err)
			}
		} else {
			exitcode = cmd.ProcessState.ExitCode()
		}

		log.Printf("%s Process with PID %d terminated, exitcode=%d, signal=%s, pmsg=%s", logMsgPrefix, cmd.Process.Pid, exitcode, sigStr, pmsg)

		// Remove the job cgroup subdir
		log.Printf("%s Removing job cgroup", logMsgPrefix)
		err = removeJobCgroup(jobID, clientName)
		if err != nil {
			pmsg = fmt.Sprintf("%s, error removing job cgroup: %v", pmsg, err)
		}

		// Update job status under lock.
		j.mu.Lock()
		defer j.mu.Unlock()
		log.Printf("%s Updating job object", logMsgPrefix)
		job.ExitCode = exitcode
		job.SignalStr = sigStr
		job.Mesg = strings.TrimSpace(fmt.Sprintf("%s %s", job.Mesg, pmsg))
		// We want to make sure that if the process was terminated by our own Stop() API, we don't step over the job's State field.
		if job.State != TerminatedByAPI {
			job.State = Completed
			job.EndedUnixMilli = time.Now().UnixMilli()
		}

		log.Printf("%s Finished", logMsgPrefix)
		wcancel()
	}()

	return jobID, nil
}

// Status returns a job object for the specified jobID, provided that the job exists and belongs to clientName.
// However, if overrideClientCheck is true, the clientName check is not performed.
// The returned job is a copy of the actual job, to ensure that callers don't tamper with the actual job object.
func (j *JobsLib) Status(ctx context.Context, clientName, jobID string, overrideClientCheck bool) (Job, error) {
	err := j.commonSanity(ctx, clientName, jobID, overrideClientCheck)
	if err != nil {
		return Job{}, fmt.Errorf("cannot get Status, sanity check failed: %w", err)
	}

	logMsgPrefix := fmt.Sprintf("JobsLib get Status: clientName=%s jobID=%s:", clientName, jobID)

	log.Printf("%s Finding job", logMsgPrefix)
	theJob, err := j.findAndCopyJobByID(ctx, jobID)
	if err != nil {
		log.Printf("%s ERROR Finding job: %v", logMsgPrefix, err)
		return Job{}, fmt.Errorf("cannot get Status, %w", err)
	}

	jclnt := theJob.ClientName
	if jclnt != clientName {
		if !overrideClientCheck {
			return Job{}, fmt.Errorf("cannot get Status, job with ID %q does not belong to client %q", jobID, clientName)
		}

		log.Printf("%s JobID=%s with ClientName=%s does not belong to client %s, but returning it anyway because overrideClientCheck=true",
			logMsgPrefix, jobID, jclnt, clientName)
	}

	log.Printf("%s Successful", logMsgPrefix)

	return theJob, nil
}

// Stop stops the job with the given jobID, provided that the job ID exists, and was started by the given clientName.
// However, if overrideClientCheck is true, the clientName check is not performed.
// It returns a copy of the job which was stopped. A copy is returned because we don't want callers to mess with
// the actual job object. On errors, it will be an empty/zero Job struct, which callers shouldn't work with.
func (j *JobsLib) Stop(ctx context.Context, clientName, jobID string, overrideClientCheck bool) (Job, error) {
	err := j.commonSanity(ctx, clientName, jobID, overrideClientCheck)
	if err != nil {
		return Job{}, fmt.Errorf("cannot Stop, sanity check failed: %w", err)
	}

	err = verifyClientCgroup(clientName)
	if err != nil {
		return Job{}, fmt.Errorf("cannot Stop, error verifying cgroup dir for %q: %w", clientName, err)
	}

	logMsgPrefix := fmt.Sprintf("JobsLib Stop: clientName=%s jobID=%s:", clientName, jobID)

	log.Printf("%s Finding job", logMsgPrefix)
	theJob, err := j.findJobByJobID(ctx, jobID)
	if err != nil {
		return Job{}, fmt.Errorf("cannot Stop, %w", err)
	}

	// check the context again, just in case (hello, TOCTTOU!)
	if ctx.Err() != nil {
		return Job{}, fmt.Errorf("cannot Stop, %w", ctx.Err())
	}

	retval, err := j.deepCopyJobFromPointer(ctx, theJob)
	if err != nil {
		return Job{}, fmt.Errorf("Failed copying job from pointer for jobID %q: %w", jobID, err)
	}

	// right, so we actually need to do some work...
	log.Printf("%s Begin Stop job operation", logMsgPrefix)
	j.mu.Lock()
	defer j.mu.Unlock()

	jclnt := theJob.ClientName
	if jclnt != clientName {
		if !overrideClientCheck {
			return Job{}, fmt.Errorf("cannot Stop, job with ID %q does not belong to client %q", jobID, clientName)
		}

		log.Printf("%s JobID=%s with ClientName=%s does not belong to client %s, but stopping it anyway because overrideClientCheck=true",
			logMsgPrefix, jobID, jclnt, clientName)
	}

	if theJob.State == TerminatedByAPI || theJob.State == Completed {
		log.Printf("%s Job (PID=%d) already stopped with state=%s at %v, nothing to do", logMsgPrefix, theJob.PID,
			theJob.State, time.UnixMilli(theJob.EndedUnixMilli).UTC())
		// Job's already stopped so there's nothing to do. This makes stop operations idempotent.
		return retval, nil
	}

	log.Printf("%s Stopping job (PID %d)", logMsgPrefix, theJob.PID)
	err = terminateJob(jobID, clientName)
	if err != nil {
		return Job{}, err
	}

	// Wait for PID to go away
	// NOTE: The Go 1.22 source for os.FindProcess() says that on Unix it never returns an error.
	// See: https://cs.opensource.google/go/go/+/refs/tags/go1.22.4:src/os/exec.go;drc=261e26761805e03c126bf3934a8f39302e8d85fb;l=88
	p, err := os.FindProcess(int(theJob.PID))
	if err != nil {
		// Just in case this changes some day...
		return Job{}, err
	}

	var perr error
	for {
		// Sometimes I really wish that Go had a "while"
		perr = p.Signal(syscall.Signal(0))
		if perr == nil {
			// It's still alive.
			// Sleep a bit so we don't busy-wait/spinlock and hog a CPU core.
			time.Sleep(10 * time.Millisecond)
		} else {
			log.Printf("%s Process with PID %d is no longer running", logMsgPrefix, theJob.PID)
			break
		}
	}

	log.Printf("%s Removing job cgroup dir", logMsgPrefix)
	err = removeJobCgroup(jobID, clientName)
	if err != nil {
		// Oh no! Let's return an error. removeJobCgroup is idempotent so the API can try again
		return Job{}, fmt.Errorf("cannot Stop, cgroup func error: %w", err)
	}

	// Update job fields, in the actual job as well as our copy.
	// We can't use deepCopyJobFromPointer() because we've already taken the write mutex.
	log.Printf("%s Updating job status", logMsgPrefix)
	theJob.State = TerminatedByAPI
	retval.State = TerminatedByAPI

	theJob.EndedUnixMilli = time.Now().UnixMilli()
	retval.EndedUnixMilli = time.Now().UnixMilli()

	theJob.Mesg = strings.TrimSpace(fmt.Sprintf("%s Stop/Termination requested by API", theJob.Mesg))
	// And again so the strings are totally separate. Yes, Black Sabbath's "Paranoid" is one of my faves :-)
	retval.Mesg = strings.TrimSpace(fmt.Sprintf("%s Stop/Termination requested by API", retval.Mesg))

	// ExitCode etc should ideally be set by the goroutine threaded off by Start(), but we'll set it here too anyway.
	theJob.ExitCode = -1 // Because our stop is a SIGKILL
	retval.ExitCode = -1

	theJob.SignalStr = "SIGKILL"
	retval.SignalStr = "SIGKILL"

	log.Printf("%s Stopped job and set job fields: state=%s, endtime=%v, exitcode=%d, sigstr=%s, mesg=%q",
		logMsgPrefix, theJob.State,
		time.UnixMilli(theJob.EndedUnixMilli).UTC(),
		theJob.ExitCode, theJob.SignalStr, theJob.Mesg)

	return retval, nil
}

// StreamOutputSetup, as the name implies, sets up the caller to perform self-service streaming of a job's
// output log. It performs some basic checks, and if all went well, it returns a copy of the job.
func (j *JobsLib) StreamOutputSetup(ctx context.Context, clientName, jobID string, overrideClientCheck bool) (Job, error) {
	err := j.commonSanity(ctx, clientName, jobID, overrideClientCheck)
	if err != nil {
		return Job{}, fmt.Errorf("cannot StreamOutputSetup, sanity check failed: %w", err)
	}

	logMsgPrefix := fmt.Sprintf("JobsLib StreamOutputSetup: clientName=%s jobID=%s:", clientName, jobID)

	log.Printf("%s Finding job", logMsgPrefix)
	theJob, err := j.findAndCopyJobByID(ctx, jobID)
	if err != nil {
		return Job{}, fmt.Errorf("cannot StreamOutputSetup, %w", err)
	}

	jclnt := theJob.ClientName
	if jclnt != clientName {
		if !overrideClientCheck {
			return Job{}, fmt.Errorf("cannot StreamOutputSetup, job with ID %q does not belong to client %q", jobID, clientName)
		}

		log.Printf("%s JobID=%s with ClientName=%s does not belong to client %s, but streaming output anyway because overrideClientCheck=true",
			logMsgPrefix, jobID, jclnt, clientName)
	}

	if ctx.Err() != nil {
		return Job{}, fmt.Errorf("cannot StreamOutputSetup, %w", ctx.Err())
	}

	// If we got here, we have a valid job. Our work is done, but the caller's work is just beginning.
	return theJob, nil
}
