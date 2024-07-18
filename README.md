# jobmaster
_Jobmaster (n) :: the keeper of a livery stable_

## What Is It?

Jobmaster is a Go (golang) proof-of-concept of a gRPC client and server for a simple job management library which can execute arbitrary Linux processes. Uses cgroups (`cgroup-v2`) to provide job-level resource quotas for CPU, memory, and IO. Client-server communication is authenticated via mTLS (TLS v1.3); a default set of X.509 certificates have been included for your delectation.

It supports multiple concurrent clients making requests to a single server. The functionality includes being able to start, stop, show status of, and stream the output of, the afore-mentioned Linux processes/jobs which are resource-limited using cgroups. Since cgroup setup requires elevated privileges, the job server necessarily requires that it be started as a privileged user.

----------------------------

## Building It

### Protobuf

```bash
cd proto/jobs/v1
protoc --go_opt=paths=source_relative \
    --go-grpc_opt=paths=source_relative \
    --go_out=../../../gen/proto/go/jobs/v1 \
    --go-grpc_out=../../../gen/proto/go/jobs/v1 \
    jobs.proto

```

The generated/compiled output is put in `api/gen/proto/go/jobs/v1`

### Server

```bash
cd cmd/server
go build -race # Omit -race if you don't want race detection
```

### Client

```bash
cd cmd/client
go build -race # Omit -race if you don't want race detection
```

-----------------------------

## Unit-testing It

Run the following command as `root` (root is required for the cgroup test) at the top of the project tree. Replace `<MAJ>:<MIN>` with the device ID of a suitable block device on your system.
```bash
go test ./... -race -v -iodev <MAJ>:<MIN>
```

-----------------------------

## Running it

### Server

The server needs to run with elevated privileges (`root`) to be able to interact with the cgroup infrastructure.

#### Usage

```bash
./server --help
Usage:
  server [OPTIONS]

Application Options:
  -o, --output-dir=  The absolute path to the directory where job output files will be written (default: /tmp)
  -d, --iodevice-id= The Linux device ID (<MAJOR>:<MINOR>) for the IO device to set cgroup limits

Help Options:
  -h, --help         Show this help message
```

The `-d`/`--iodevice-id` option is mandatory.

#### Example Invocation

```bash
cd cmd/server
./server -d 7:23 /tmp
```

### Client

The client does not need elevated privileges to run. Client invocations will work with jobs/process which are started on the server.

#### Usage

```bash
./client --help
Usage: client -n CLIENT_NAME OPERATION [OP_ARGS...]
A gRPC client which executes arbitrary Linux commands on the jobmaster gRPC server.

Arguments:
  -n, --name CLIENT_NAME
      MANDATORY. The name of user/client with which you wish to invoke the RPCs.
	  -n/--name and CLIENT_NAME MUST be first two arguments to the program.

  OPERATION
    MANDATORY. The operation corresponding to the RPC call to make. This is one of:
      start           start/execute a command on the RPC server.

	  stop            stop a running command on the server.

	  status          show the status of a command previously executed on the server.

	  streamoutput    stream the output of a job whch was previously started. If the
   	                  job is still running, the RPC call will wait until the server indicates
		              that the job has finished running and that there will be no more output.
					  If the job has completed running (or was terminated), the call will finish
			          after streaming all available output from the stopped/terminated job.

  OP_ARGS
    The arguments to OPERATION. The JOB_ID is case-sensitive.

	  OPERATION start
	    COMMAND [ARGS]  The COMMAND to run on the server, along with ARGS required for it.

	  OPERATION stop
	    JOB_ID          The job ID of a job which was previously started.

	  OPERATION status
	    JOB_ID          The job ID of a job which was previously started.

	  OPERATION streamoutput
	    JOB_ID          The job ID of a job which was previously started.

Note that the name and port of the server to connect to, are not currently configurable.
They will be made configurable in a future release.

Examples:
  client --name client1 start dd if=/dev/urandom of=/dev/null bs=1M count=100
  client -n client1 start ls
  client -n client1 stop <JOBID>
  client -n client1 status <JOBID>
  client -n client1 streamoutput <JOBID>
```

#### Sample Run

```bash
$ ./client -n client1 start ls -l /etc
2i3EUtHSaBSsWNu7KfHU7KRqNdK

$ ./client -n client1 status 2i3Eg4wqylE7svkUSSyhbUBDNvT
id=2i3EUtHSaBSsWNu7KfHU7KRqNdK,jobMsg=,client=superuser,pid=25070,state=1,exitCode=0,signalstr=,cmd=command:"ls",args="-l" args="/etc",started=2024-06-18 11:41:19.135 +0000 UTC,ended=1970-01-01 00:00:00 +0000 UTC

# NOTE: state=1 along with ended= showing the start of the Unix epoch indicates that the job was still running

$ ./client -n client1 streamoutput 2i3Eg4wqylE7svkUSSyhbUBDNvT
<LIST_OF_FILES_ON_SERVER'S_/etc_DIR>

$ client -n client1 stop 2i3Eg4wqylE7svkUSSyhbUBDNvT
id=2i3EUtHSaBSsWNu7KfHU7KRqNdK,jobMsg=Stop/Termination requested by API ,client=superuser,pid=25070,state=2,exitCode=-1,signalstr=killed,cmd=command:"ls",args="-l" args="/etc",started=2024-06-18 11:41:19.135 +0000 UTC,ended=2024-06-18 11:41:20.001 +0000 UTC
```

The `stop` operation is idempotent - running it on an already-stopped job will just return the details of that job.

The `state` field of the status output can take values from 0 to 3, where:
- `0` - Unspecified (this is usually not seen in normal operation).
- `1` - Running.
- `2` - Stopped or force-terminated, either by an explicit stop operation or by the job being killed by the kernel due to having exceed the set cgroup quota/limits. The `exitCode` field will be set to `-1` in this case.
- `3` - Completed normally. The `exitCode` field will contain whatever exit code the process completed with. A successful completion without errors usually reads `0` in the `exitCode` field.

#### Pre-configured Clients And Their Roles

The server code includes the following list of client names (currently hardcoded, but there is a `TODO` item to make this configurable) which are present and can be used for the `-n`/`--name` command line arg to the client:
- `client1` through `client3` are "regular" users. A job started by client_X_ cannot be interacted with by client_NOT-X_.
- `superuser` is, as the name implies, a super/"admin" user. This user can interact with jobs from other clients.

---------------------------

## Code Layout

```
jobmaster
    ├── api
    │   ├── authn                ---> Simple authentication for the mTLS connection
    │   ├── authz                ---> Simple user authorization
    │   ├── client               ---> The client-side implementation of the gRPC API
    │   ├── gen                  ---> The compiled/generated protobuf Go sources
    │   │   └── proto
    │   │       └── go
    │   │           └── jobs
    │   │               └── v1
    │   ├── proto                ---> The source protobuf definition
    │   │   └── jobs
    │   │       └── v1
    │   └── server               ---> The server-side implementation of the gRPC API
    ├── certs                    ---> Pre-generated X.509 certificates for mTLS
    │   └── BADCERTS-For-Testing ---> Invalid/insecure certificates used in the unit test
    │       ├── expired
    │       ├── unknownclient
    │       ├── weakec
    │       └── weaksha
    ├── cmd           
    │   ├── client               ---> Code for the client binary
    │   └── server               ---> Code for the server binary
    └── jobslib                  ---> The job library used for job management

```

`TODO` comments in the code delineate items/enhancements/improvements which will be handled at a future date. One of which is this README :-)
 - A major `TODO` is to remove the "read job/process output" logic from the client, and make it a part of the job library itself.

Core funtionality (including the file watcher) uses only the Go standard library. Only two direct external dependencies are used:
- `github.com/jessevdk/go-flags` for providing command-line argument handling for the server binary.
- `github.com/segmentio/ksuid` for generating job IDs (which are [KSUIDs](https://github.com/segmentio/ksuid/blob/master/README.md))
