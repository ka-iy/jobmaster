package jobslib

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// Command-line arg for device ID
var ioDevID = flag.String("iodev", "", "Linux block device ID in <MAJ>:<MIN> format to use for setting IO cgroup quota")

// Run example (replace the param for iodev with a suitable block device ID):
//    go test -race -v -iodev <MAJ>:<MIN>

// TODO: Messages for requires/asserts

func TestCgroups(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatal("Please run this test as the root user on a Linux system.")
	}

	defer goleak.VerifyNone(t)

	var err error

	require.NotEmpty(t, *ioDevID, "Require Linux block device ID in <MAJ>:<MIN> format")

	clientList := []string{
		"client1",
		"client2",
		"superuser",
	}
	jobID := ""
	jd := ""
	clientName := clientList[1]
	mmnt := filepath.Join(cgroup2MountRoot, cgroupParentDir)
	c1d := filepath.Join(mmnt, clientList[0])
	c2d := filepath.Join(mmnt, clientList[1])
	sud := filepath.Join(mmnt, clientList[2])

	t.Run("Prerequisites", func(t *testing.T) {
		require.DirExists(t, cgroup2MountRoot)
		require.NoDirExists(t, mmnt)
		assert.NoDirExists(t, c1d)
		assert.NoDirExists(t, c2d)
		assert.NoDirExists(t, sud)
	})

	t.Run("Setup", func(t *testing.T) {
		// Setup
		err = setupJobmasterCgroups(clientList)
		require.NoError(t, err)

		assert.DirExists(t, mmnt)
		assert.DirExists(t, c1d)
		assert.DirExists(t, c2d)
		assert.DirExists(t, sud)
	})

	t.Run("AddJobCgroup", func(t *testing.T) {
		// Add a job-level cgroup
		jobID = "deadbeefcafe"
		jd = filepath.Join(mmnt, clientName, jobID)

		assert.NoDirExists(t, jd)

		jd2, err := createJobCgroup(jobID, clientName, *ioDevID)
		require.NoError(t, err)
		assert.DirExists(t, jd2)
		assert.Equal(t, jd, jd2)

		// Check that the job cgroup interface files contain the required settings
		ioLimits := fmt.Sprintf("%s %s %s\n", *ioDevID, ioMaxLimitBytesPerSec, ioMaxLimitIOPS)
		ioFile := filepath.Join(jd, ioMaxLimitFile)
		assert.FileExists(t, ioFile)
		data, err := os.ReadFile(ioFile)
		assert.NoError(t, err)
		assert.Equal(t, ioLimits, string(data))

		// TODO: Check other job-level cgroup interface files
	})

	t.Run("Teardown", func(t *testing.T) {
		// Remove the job cgroup directory
		err = removeJobCgroup(jobID, clientName)
		assert.NoError(t, err)
		assert.NoDirExists(t, jd)

		// Teardown
		err = TeardownJobmasterCgroups(clientList)
		require.NoError(t, err)
		assert.NoDirExists(t, c1d)
		assert.NoDirExists(t, c2d)
		assert.NoDirExists(t, sud)
		assert.NoDirExists(t, mmnt)
	})
}
