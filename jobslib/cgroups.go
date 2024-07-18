package jobslib

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// TODO: Verify that cgroup-v2 is the cgroups version on the system and that it is not in unified mode.

// Constants for cgroup creation.
const (
	// cgroup2MountRoot is the main system mountpoint of cgroup-v2.
	cgroup2MountRoot = "/sys/fs/cgroup"

	// cgroupSubtreeCtrlFilename is the name of the cgroup file which contains the list of enabled
	// cgroup controllers for a cgroup subtree.
	cgroupSubtreeCtrlFilename = "cgroup.subtree_control"

	// CGroupControllerEnableString is what will be written to the cgroup.subtree_control file to
	// enable the CPU, IO, and memory cgroup controllers. TODO: Get this from a config.
	cgroupControllerEnableString = "+io +cpu +memory"

	// cgroupParentDir is the root of the cgroup hierarchy under which the client-level and job-level
	// cgroup directories will be created. TODO: Get this from a config.
	cgroupParentDir = "jobmaster"

	// cgroupKillFilename is the name of the cgroup file which, when 1 is written to it, will
	// terminate all processes (including any spawned child processes) in that cgroup.
	cgroupKillFilename = "cgroup.kill"
)

// Constants for per-job cgroup limits.
// TODO: Get these from a config.
const (
	// cpuMaxLimitMicrosecs is the CPU bandwidth consumption limit for a job. The first value is
	// the allowed time quota in microseconds for which all processes collectively in a child
	// group can run during one period. The second value specifies the length of the period.
	cpuMaxLimitMicrosecs = "10000 10000"

	// cpuMaxLimitFile is the cgroup-v2 CPU interface file in which we set cpuMaxLimitMicrosecs.
	cpuMaxLimitFile = "cpu.max"

	// memoryMaxLimitBytes is the hard limit, in bytes, for the memory which a job may take.
	// If a cgroup’s memory usage reaches this limit and can’t be reduced, the OOM killer is
	// invoked in the cgroup if other special conditions don't apply. See the cgroup-v2 kernel docs.
	memoryMaxLimitBytes = "1048576"

	// memoryMaxLimitFile is the cgroup-v2 memory interface file in which memoryMaxLimitBytes is set.
	memoryMaxLimitFile = "memory.max"

	// memoryOOMGroup is to treat memory-related OOMs as an indivisible unit. If set to 1, any OOMs
	// invoked on a process in the job cgroup will OOM-kill all processes in the job cgroup. This
	// ensures that any processes which the main job spawns, are also OOM-killed.
	memoryOOMGroup = "1"

	// memoryOOMGroupFile is the cgroup-v2 interface file related to treating cgroup jobs as an
	// indivisible group for the purposes of OOM-killing.
	memoryOOMGroupFile = "memory.oom.group"

	// ioMaxLimitBytesPerSec is the limit, in bytes per second, of how much IO the specified device
	// can read from or write to the disk. Traffic exceeding these limits will be delayed/throttled,
	// although the cgroup-v2 documentation says that temporary bursts are allowed.
	ioMaxLimitBytesPerSec = "rbps=4096 wbps=4096"

	// ioMaxLimitIOPS is the maximum read/write IOs per second allowed for the specified device. IO
	// traffic exceeding these limits will be delayed/throttled, although the cgroup-v2 documentation
	// says that temporary bursts are allowed.
	ioMaxLimitIOPS = "riops=10 wiops=10"

	// ioMaxLimitFile is the cgroup-v2 interface file in which ioMaxLimitBytesPerSec and ioMaxLimitIOPS
	// will be written.
	ioMaxLimitFile = "io.max"
)

// setupJobmasterCgroups sets up the cgroup directory hierarchy (up to the client level) for jobmaster.
// The jobmaster cgroup hierarchy is created under the main system cgroup dir rooted at cgroup2MountRoot.
// So the hierarchy will be:
//
//	cgroup2MountRoot -> cgroupParentDir -> <clients in clientList>
//
// Each job will then be created as a subdirectory under its relevant client.
func setupJobmasterCgroups(clientList []string) error {
	clen := len(clientList)
	if clen == 0 {
		return errors.New("client list for cgroup creation is empty")
	}

	var err error

	cpdPath := filepath.Join(cgroup2MountRoot, cgroupParentDir)
	log.Printf("Setting up jobmaster cgroup hierarchy at: %s", cpdPath)
	err = os.MkdirAll(cpdPath, 0755) // We use MkdirAll to not error out if it exists
	if err != nil {
		return fmt.Errorf("failed to create parent cgroup path %q: %w", cgroupParentDir, err)
	}

	// Now enable the required controllers in the cgroup.subtree_control file.
	// TODO: First, check that cgroupSubtreeCtrlFilename exists.
	cscFile := filepath.Join(cpdPath, cgroupSubtreeCtrlFilename)
	err = os.WriteFile(cscFile, []byte(cgroupControllerEnableString), 0644)
	if err != nil {
		err = errors.Join(fmt.Errorf("error enabling cgroup controllers on %q: %w", cscFile, err))
		err2 := os.Remove(cpdPath)
		if err2 != nil {
			err = errors.Join(fmt.Errorf("error removing %q %w", cpdPath, err))
		}

		return err
	}

	// Now create the per-client cgroup subdirs
	var errList []error
	for _, clientName := range clientList {
		// Create it.
		relPath := filepath.Join(cpdPath, clientName)
		err = os.MkdirAll(relPath, 0755)
		if err != nil {
			errList = append(errList, fmt.Errorf("error creating client cgroup dir for client %q due to %w", clientName, err))
			break
		}
		// Now enable the required controllers in the cgroup.subtree_control file.
		ccscFile := filepath.Join(relPath, cgroupSubtreeCtrlFilename)
		err = os.WriteFile(ccscFile, []byte(cgroupControllerEnableString), 0644)
		if err != nil {
			emsg := fmt.Errorf("error enabling cgroup controllers on %q: %w", ccscFile, err)
			err2 := syscall.Rmdir(relPath)
			if err2 != nil {
				emsg = errors.Join(fmt.Errorf("there was also an error removing %q %w", relPath, err2))
			}

			errList = append(errList, emsg)

			break
		}
		// TODO: Check that the controllers are enabled.
	}

	if len(errList) > 0 {
		finale := errors.New("error creating client cgroup dir(s)")
		for _, e := range errList {
			finale = errors.Join(e)
		}

		// Clean up
		err = TeardownJobmasterCgroups(clientList)
		if err != nil {
			// We just can't catch a break, can we?
			finale = errors.Join(fmt.Errorf("error cleaning up: %w", err))
		}

		return finale
	}

	log.Printf("Verifying creation of jobmaster cgroup hierarchy at: %s", cpdPath)
	err = verifyCgroups(clientList)
	if err != nil {
		return err
	}

	// Phew!
	log.Printf("Successful: Setting up jobmaster cgroup hierarchy at: %s", cpdPath)
	return nil
}

// verifyCgroups checks whether the jobmaster cgroup hierarchy exists in the system's
// cgroup-v2 sysfs.
func verifyCgroups(clientList []string) error {
	if len(clientList) == 0 {
		return errors.New("client list for cgroup verification is empty")
	}

	mainDir := filepath.Join(cgroup2MountRoot, cgroupParentDir)
	mdInfo, err := os.Stat(mainDir)
	if err != nil {
		return fmt.Errorf("error verifying main jobmaster cgroup directory '%s': %w", mainDir, err)
	}
	if !mdInfo.IsDir() {
		return fmt.Errorf("main jobmaster cgroup directory '%s' is not a directory", mainDir)
	}

	for _, clientName := range clientList {
		if err := verifyClientCgroup(clientName); err != nil {
			return err
		}
	}

	return nil
}

// verifyClientCgroup checks whether the jobmaster cgroup hierarchy properly includes
// an entry for clientName in the system's cgroup-v2 sysfs.
func verifyClientCgroup(clientName string) error {
	if clientName == "" || strings.TrimSpace(clientName) == "" {
		return errors.New("client name is empty")
	}

	clDir := filepath.Join(cgroup2MountRoot, cgroupParentDir, clientName)
	cdInfo, err := os.Stat(clDir)
	if err != nil {
		return fmt.Errorf("error verifying client cgroup dir '%s': %w", clDir, err)
	}
	if !cdInfo.IsDir() {
		return fmt.Errorf("cgroup file '%s' for client %s is not a directory", clDir, clientName)
	}

	return nil
}

// TeardownJobmasterCgroups tears down the jobmaster cgroup hierarchy after first terminating any running
// client job/process for each jobmaster client in clientList. The workingDir passed in should be the
// same as that passed to SetupJobmasterCgroups().
func TeardownJobmasterCgroups(clientList []string) error {
	clen := len(clientList)
	if clen == 0 {
		return errors.New("client list for cgroup teardown is empty")
	}

	var err error

	cpdPath := filepath.Join(cgroup2MountRoot, cgroupParentDir)
	log.Printf("Tearing down jobmaster cgroup hierarchy at: %s", cpdPath)
	// Now go through each client subdir and write 1 to its cgroup.kill file, then remove the
	// client cgroup dir. Note that if this is not done, the client dirs will stick around in
	// the main /sys/fs/cgroup dir.
	var errList []error
	for _, clientName := range clientList {
		clientDirPath := filepath.Join(cpdPath, clientName)

		_, err := os.Stat(clientDirPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			} else {
				return err
			}
		}

		killfilePath := filepath.Join(clientDirPath, cgroupKillFilename)
		err = os.WriteFile(killfilePath, []byte("1"), 0644)
		if err != nil {
			errList = append(errList, err)
		}

		// Now descend into clientDirPath and remove all job directories. We need to perform
		// the descent because os.Remove() / os.RemoveAll() error out with cgroup subdirs
		// due to the cgroup interface files, and syscall.Rmdir() won't remove a directory
		// if it contains subdirectories.
		entries, err := os.ReadDir(clientDirPath)
		if err != nil {
			errList = append(errList, err)
		} else {
			for _, entry := range entries {
				if entry.IsDir() {
					ePath := filepath.Join(clientDirPath, entry.Name())
					err = syscall.Rmdir(ePath)
					if err != nil {
						errList = append(errList, err)
					}
				}
			}
		}

		// Now remove the main client cgroup directory
		err = syscall.Rmdir(clientDirPath)
		if err != nil {
			errList = append(errList, err)
		}
	}

	if len(errList) > 0 {
		finale := errors.New("error removing one or more client cgroup (sub)dirs")
		for _, e := range errList {
			finale = errors.Join(e)
		}

		return finale
	}

	// Now remove the main jobmaster cgroup subdir
	err = syscall.Rmdir(filepath.Join(cgroup2MountRoot, cgroupParentDir))
	if err != nil {
		return fmt.Errorf("failed to remove jobmaster cgroup parent dir %q: %w", cgroupParentDir, err)
	}

	log.Printf("Successful: Tearing down jobmaster cgroup hierarchy at: %s", cpdPath)
	return nil
}

// createJobCgroup creates a cgroup for a job (based on jobID), then sets the per-job cgroup quotas/limits.
// devMajorMinor is a device ID of the form "major:minor" and is used to set the IO quotas. The job cgroup
// directory is created under the client-level cgroup directory specified by clientName, which will already
// exist, having been created by SetupJobmasterCgroups(). Note that devMajorMinor needs to be the ID of a
// valid block device on the machine where this runs. It returns the full path to the job cgroup subdir.
func createJobCgroup(jobID, clientName, devMajorMinor string) (string, error) {
	// Sanity
	if jobID == "" {
		return "", errors.New("jobID for job cgroup creation is empty")
	}
	if clientName == "" {
		return "", fmt.Errorf("clientName for job cgroup creation is empty for jobID %s", jobID)
	}
	if devMajorMinor == "" {
		return "", fmt.Errorf("device string devMajorMinor for job cgroup creation is empty for jobID %s and client %s", jobID, clientName)
	}

	var err error

	// Create the cgroup dir for the job
	jcPath := filepath.Join(cgroup2MountRoot, cgroupParentDir, clientName, jobID)
	err = os.Mkdir(jcPath, 0755) // Everything up to the jobID component must already exist, and jobID itself shouldn't
	if err != nil {
		return "", fmt.Errorf("failed to create job cgroup for jobID %s and client %s: %w", jobID, clientName, err)
	}

	// Set the IO limits for this job
	ioLimits := fmt.Sprintf("%s %s %s", devMajorMinor, ioMaxLimitBytesPerSec, ioMaxLimitIOPS)
	ioFile := filepath.Join(jcPath, ioMaxLimitFile)
	err = os.WriteFile(ioFile, []byte(ioLimits), 0644)
	if err != nil {
		return "", fmt.Errorf("error writing to IO limit file %q for jobID %s and client %s: %w", ioFile, jobID, clientName, err)
	}

	// Set the memory limits
	memMaxFile := filepath.Join(jcPath, memoryMaxLimitFile)
	err = os.WriteFile(memMaxFile, []byte(memoryMaxLimitBytes), 0644)
	if err != nil {
		return "", fmt.Errorf("error writing to memory max limit file %q for jobID %s and client %s: %w", memMaxFile, jobID, clientName, err)
	}
	memOGFile := filepath.Join(jcPath, memoryOOMGroupFile)
	err = os.WriteFile(memOGFile, []byte(memoryOOMGroup), 0644)
	if err != nil {
		return "", fmt.Errorf("error writing to memory OOM group file %q for jobID %s and client %s: %w", memOGFile, jobID, clientName, err)
	}

	// Set CPU limits
	cpuFile := filepath.Join(jcPath, cpuMaxLimitFile)
	err = os.WriteFile(cpuFile, []byte(cpuMaxLimitMicrosecs), 0644)
	if err != nil {
		return "", fmt.Errorf("error writing to CPU max limit file %q for jobID %s and client %s: %w", cpuFile, jobID, clientName, err)
	}

	return jcPath, nil
}

// terminateJob kills all running processes spawned for the given jobID.
func terminateJob(jobID, clientName string) error {
	// Sanity
	if jobID == "" {
		return errors.New("jobID for job termination is empty")
	}
	if clientName == "" {
		return fmt.Errorf("clientName for job termination is empty for jobID %s", jobID)
	}

	jobDirPath := filepath.Join(cgroup2MountRoot, cgroupParentDir, clientName, jobID)

	// Terminate any running processes
	killfilePath := filepath.Join(jobDirPath, cgroupKillFilename)
	err := os.WriteFile(killfilePath, []byte("1"), 0644)
	if err != nil {
		// No kill file means that someone already took care of it for us.
		if os.IsNotExist(err) {
			return nil
		} else {
			return fmt.Errorf("stat error for %s when terminating job %s: %w", jobDirPath, jobID, err)
		}
	}

	return nil
}

// removeJobCgroup removes a cgroup subdir for a job belonging to a client. It will write to the
// cgroup killfile first, to make sure that any processes still running in the cgroup are terminated
// before the cgroup is removed.
func removeJobCgroup(jobID, clientName string) error {
	// Our sanity is handled, insofar as it can be, by calling terminateJob
	err := terminateJob(jobID, clientName)
	if err != nil {
		return err
	}

	jobDirPath := filepath.Join(cgroup2MountRoot, cgroupParentDir, clientName, jobID)
	_, err = os.Stat(jobDirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Nothing to do
		} else {
			return fmt.Errorf("stat error for %s when terminating job %s: %w", jobDirPath, jobID, err)
		}
	}

	// Remove the directory
	err = syscall.Rmdir(jobDirPath)
	if err != nil {
		return fmt.Errorf("failed to remove job cgroup dir %q: %w", jobDirPath, err)
	}

	return nil
}
