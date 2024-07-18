package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"jobmaster/api/authz"
	jobsv1 "jobmaster/api/gen/proto/go/jobs/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// NOTE: grpc.status v1.64.0 uses fmt.Sprintf for status.Errorf, not fmt.Errorf. So errors can't be wrapped.
// TODO: Better mapping of job library errors to proper gRPC status codes.

// Start implements the Start unary gRPC API, which starts the job/command in JobCreateRequest.
func (j *JobServiceServer) Start(ctx context.Context, in *jobsv1.JobCreateRequest) (*jobsv1.JobID, error) {
	clientName, clientRole, err := ClientInfoFromContext(ctx)
	if err != nil {
		log.Printf("ERROR: RPC Start: Error getting client info from context: %v", err)
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}

	// Before we do any work, check whether the context is done/ended from the client
	if ctx.Err() != nil {
		log.Printf("RPC Start: Unary context error for %s %s: %v", clientRole, clientName, ctx.Err())
		return nil, status.Error(codes.Aborted, ctx.Err().Error())
	}

	command := in.GetCommand()
	args := in.GetArgs()
	// Make the call
	log.Printf("RPC Start: %s %s: command=%s, args=%+v", clientRole, clientName, command, args)
	jobID, err := j.JobMgr.Start(ctx, clientName, command, args)
	if err != nil {
		log.Printf("ERROR: RPC Start for %s %s: Job library call returned error: %v", clientRole, clientName, err)
		return nil, status.Error(codes.Internal, err.Error())
	}
	respData := fmt.Sprintf("Start RPC was processed for %s %s with JobID %s for command=%s args=%+v", clientRole, clientName, jobID, command, args)
	log.Println(respData)
	return &jobsv1.JobID{JobID: jobID}, nil
}

// Stop implements the Stop Unary gRPC API.
func (j *JobServiceServer) Stop(ctx context.Context, in *jobsv1.JobIDRequest) (*jobsv1.JobResponse, error) {
	clientName, clientRole, err := ClientInfoFromContext(ctx)
	if err != nil {
		log.Printf("ERROR: RPC Stop: Error getting client info from context: %v", err)
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}

	// Before we do any work, check whether the context is done/ended from the client
	if ctx.Err() != nil {
		log.Printf("RPC Stop: Unary context error for %s %s: %v", clientRole, clientName, ctx.Err())
		return nil, status.Error(codes.Aborted, ctx.Err().Error())
	}

	jobID := in.GetJobIDRequest().GetJobID()
	log.Printf("RPC Stop: request from %s %s for job ID %s", clientRole, clientName, jobID)

	// Call the job library
	adminRole := (authz.RoleStrToRole(clientRole) == authz.Admin)
	job, err := j.JobMgr.Stop(ctx, clientName, jobID, adminRole)
	if err != nil {
		log.Printf("ERROR: RPC Stop for %s %s, jobID=%s: Job library call returned error: %v", clientRole, clientName, jobID, err)
		return nil, status.Error(codes.Internal, err.Error())
	}

	log.Printf("Stop RPC was processed for %s %s with Job ID %s", clientRole, clientName, jobID)
	jobIDObj := in.GetJobIDRequest()
	cmdAndArgs := &jobsv1.JobCreateRequest{
		Command: job.Command,
		Args:    job.CmdArgs,
	}
	return &jobsv1.JobResponse{
		JobIDResponse: jobIDObj,
		JobMsg:        job.Mesg,
		Client:        job.ClientName,
		Pid:           job.PID,
		State:         jobsv1.JobState(job.State),
		ExitCode:      int32(job.ExitCode),
		SignalStr:     job.SignalStr,
		Cmd:           cmdAndArgs,
		Started:       timestamppb.New(time.UnixMilli(job.StartedUnixMilli).UTC()),
		Ended:         timestamppb.New(time.UnixMilli(job.EndedUnixMilli).UTC()),
	}, nil
}

// Status implements the Status Unary gRPC API.
func (j *JobServiceServer) Status(ctx context.Context, in *jobsv1.JobIDRequest) (*jobsv1.JobResponse, error) {
	clientName, clientRole, err := ClientInfoFromContext(ctx)
	if err != nil {
		log.Printf("ERROR: RPC Status: Error getting client info from context: %v", err)
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}

	// Before we do any work, check whether the context is done/ended from the client
	if ctx.Err() != nil {
		log.Printf("RPC Status: Unary context error for %s %s: %v", clientRole, clientName, ctx.Err())
		return nil, status.Error(codes.Aborted, ctx.Err().Error())
	}

	jobIDObj := in.GetJobIDRequest()
	jobID := jobIDObj.GetJobID()
	log.Printf("RPC Status: request from %s %s for job ID %s", clientRole, clientName, jobID)

	// Call the job library
	adminRole := (authz.RoleStrToRole(clientRole) == authz.Admin)
	job, err := j.JobMgr.Status(ctx, clientName, jobID, adminRole)
	if err != nil {
		log.Printf("ERROR: RPC Status for %s %s, jobID=%s: Job library call returned error: %v", clientRole, clientName, jobID, err)
		return nil, status.Error(codes.Internal, err.Error())
	}

	log.Printf("Status RPC was processed for %s %s for JobID %s", clientRole, clientName, jobID)

	cmdAndArgs := &jobsv1.JobCreateRequest{
		Command: job.Command,
		Args:    job.CmdArgs,
	}
	return &jobsv1.JobResponse{
		JobIDResponse: jobIDObj,
		JobMsg:        job.Mesg,
		Client:        job.ClientName,
		Pid:           job.PID,
		State:         jobsv1.JobState(job.State),
		ExitCode:      int32(job.ExitCode),
		SignalStr:     job.SignalStr,
		Cmd:           cmdAndArgs,
		Started:       timestamppb.New(time.UnixMilli(job.StartedUnixMilli).UTC()),
		Ended:         timestamppb.New(time.UnixMilli(job.EndedUnixMilli).UTC()),
	}, nil
}

// StreamOutput implements the StreamOutput server-side streaming gRPC API.
func (j *JobServiceServer) StreamOutput(in *jobsv1.JobIDRequest, ss jobsv1.JobService_StreamOutputServer) error {
	ctx := ss.Context()
	clientName, clientRole, err := ClientInfoFromContext(ctx)
	if err != nil {
		log.Printf("ERROR: RPC StreamOutput: error getting client info from context: %v", err)
		return status.Error(codes.PermissionDenied, err.Error())
	}

	// Before we do any work, check whether the context is done/ended from the client
	if ctx.Err() != nil {
		log.Printf("RPC StreamOutput: Unary context error for %s %s: %v", clientRole, clientName, ctx.Err())
		return status.Error(codes.Aborted, ctx.Err().Error())
	}

	jobID := in.GetJobIDRequest().GetJobID()
	log.Printf("RPC: StreamOutput request from %s %s with Job ID: %s", clientRole, clientName, jobID)

	// First, make a call to the helper and see if the job is present and accessible
	adminRole := (authz.RoleStrToRole(clientRole) == authz.Admin)
	theJob, err := j.JobMgr.StreamOutputSetup(ctx, clientName, jobID, adminRole)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}

	logfile := theJob.OutputFilePath
	// See if we have it yet. it'll give up after a few tries
	err = j.JobMgr.WaitForFile(ctx, logfile)
	if err != nil {
		return status.Error(codes.NotFound, err.Error())
	}

	fp, err := os.OpenFile(logfile, os.O_RDONLY, 0644)
	if err != nil {
		log.Printf("RPC StreamOutput: Job ID %s: File open error: %v", jobID, err)
		return status.Error(codes.Internal, err.Error())
	}
	defer fp.Close()

	chunklenBytes := 1024 // TODO: Make this configurable
	currPos := int64(0)
	buffer := make([]byte, chunklenBytes)
	bytesread := 0

	jstate := theJob.State.String()

	if jstate == "TerminatedByAPI" || jstate == "Completed" {
		log.Printf("RPC StreamOutput: Job ID %s has state=%s, reading all output", jobID, jstate)
		// The job is done, we just need to read all its output
		for {
			// Always check whether the context was canceled
			select {
			case <-ctx.Done():
				log.Printf("WARNING: RPC StreamOutput: Job ID %s: CANCELED (context done)", jobID)
				return status.Error(codes.Canceled, ctx.Err().Error())
			default:
			}
			bytesread, err = fp.ReadAt(buffer, currPos)
			if bytesread > 0 {
				currPos += int64(bytesread)
				// We have data to send. Send it!
				output := &jobsv1.JobOutputResponse{
					Output: buffer[:bytesread],
				}
				serr := ss.Send(output)
				if serr != nil {
					log.Printf("ERROR RPC StreamOutput request from %s %s with Job ID: %s: send error: %v", clientRole, clientName, jobID, err)
					return status.Error(codes.Internal, err.Error())
				}
			}

			if err != nil {
				if errors.Is(err, io.EOF) {
					log.Printf("RPC StreamOutput request from %s %s with Job ID %s is complete.", clientRole, clientName, jobID)
					return nil
				}
			} else {
				log.Printf("RPC StreamOutput: Job ID %s: File read error: %v", jobID, err)
				return status.Error(codes.Internal, err.Error())
			}
			// And we'll keep looping until we get the job done.
		}
	}

	// If we got here, the job was not yet terminated when we got it, so we need to check the watcher
	for {
		select {
		case <-ctx.Done():
			log.Printf("WARNING: RPC StreamOutput: Job ID %s: CANCELED (context done)", jobID)
			return status.Error(codes.Canceled, ctx.Err().Error())
		default:
		}
		j.JobMgr.CvEvent.L.Lock()
		for bytesread = 0; bytesread == 0; bytesread, err = fp.ReadAt(buffer, currPos) {
			if j.JobMgr.WatcherLock.TryLock() {
				j.JobMgr.WatcherLock.Unlock()
				break
			}
			j.JobMgr.CvEvent.Wait()
		}
		j.JobMgr.CvEvent.L.Unlock()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				// uh oh
				log.Printf("ERROR: RPC StreamOutput: Job ID %s: File read error: %v", jobID, err)
				return status.Error(codes.Internal, err.Error())
			}
			// it's an EOF. Check whether we'll get anything more to read
			if j.JobMgr.WatcherLock.TryLock() {
				j.JobMgr.WatcherLock.Unlock()
				log.Printf("RPC StreamOutput: Job ID %s: Completed (EOF, job previously terminated or watcher done)", jobID)
				return nil
			}
		}
		// Keep going until EOF or other error
		if bytesread > 0 {
			currPos += int64(bytesread)
			// We have data to send. Send it!
			output := &jobsv1.JobOutputResponse{
				Output: buffer[:bytesread],
			}
			err := ss.Send(output)
			if err != nil {
				log.Printf("ERROR RPC StreamOutput request from %s %s with Job ID: %s: %v", clientRole, clientName, jobID, err)
				return status.Error(codes.Internal, err.Error())
			}
		}
	}
}
