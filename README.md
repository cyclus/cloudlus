
Cloudlus
=========

This is a collection of tools for running Cyclus simulations in the cloud and
in high-throughput computing environments.  After installing the Go toolchain
(http://golang.org/doc/install) and setting your `GOPATH` - likely available
as a standard package in your favorite package manager - all you need to do
is:

```bash
go get github.com/rwcarlsen/cloudlus/...
```

You'll want `GOPATH/bin` to be in your `PATH`. You will then be able to use a
few cli tools described below.

The primary tool provided is the `cloudlus` command. It provides the ability
to run a remote execution server.  This server handles the dispatch and
distribution of cyclus jobs to *workers* that can also be deployed with this
command.  There can be an arbitrary number of workers and they can live
locally on the same machine as the server or somewhere else in the nets.
Cyclus jobs can be deployed and fetched to/from the server using the
`cloudlus` command.  The server also provides a simple RESTful api.

CLI tools
----------

`cloudlus` is the primary command/tool provided by this package.
With its several subcommands, it can be used to:

* Deploy a remote execution server.

* Deploy remote execution workers that fetch work and push results to a
deployed server.

* Submit/retrieve jobs to/from a running remote execution server.

All subcommands have a "global" option flag `-addr=[ip:port]`.  This
specifies the remote execution server address.  By default, workers will run
*any* command sent to them - there is no sandboxing. It is recommended that
either you provide a whitelist of approved commands or run workers in a
sandboxed/container type environment.

To run a remote execution server:

```bash
cloudlus -addr=0.0.0.0:80 serve -host=my.domain.com -cache=200 -dblimit=1000
```

This runs a remote execution server on port 80 with a 200 MB in-memory job
cache and an on-disk job results database of up to 1 GB.  Job results are
purged on an LRU basis.  If the server dies, or is restarted, it reloads job
history from the existing on-disk database and requeues previously unfinished
jobs.  The server provides a super-simple dashboard at `[host]/` that show the
most recent jobs and their status.  Stdout+stderr can be viewed for each job
by clicking the corresponding link in the *status* column.  A job's output
files can be retrieved as a zip file by clicking the corresponding link in the
*output* column.  If the job was a default cyclus input file run, clicking on
the job-id link shows the input file.

To run a worker for the server:

```bash
cloudlus -addr=my.domain.com:80 work -interval=3s -whitelist=cyclus
```

This worker will poll the remote execution server at `my.domain.com` every 3
seconds for work when idle.  And the worker will only run the `cyclus`
command. Jobs with other commands will be rejected.

Jobs can also be submitted:

```bash
cloudlus submit job1.json job2.json
```

This will submit 2 jobs, for which there must be existing json files.  The
structure of these files exactly corresponds to the REST api request body for
submitting jobs (described below).  There is also a shortcut for directly
submitting Cyclus input files to run as jobs:

```bash
cloudlus submit-infile my-sim.xml
```

By default commands for submitting jobs are synchronous and won't finish until
the job is complete and results are returned.  Results are downloaded into
files named uniquely using the submitted job id's in the form
`result-[jobid].json`.

Output files from these results can be unpacked into directories named in the
form `files-[jobid]` using the unpack command:

```bash
cloudlus unpack result-[jobid].json result-[anotherjobid].json
```

REST api
----------

The api consists of the following endpoints:

* GET to `[host]/api/v1/job/[job-id]` returns a JSON object in the response
  body with all known information about the job including any output
  data+files if it finished running.  If the job has not finnished running
  yet, you can check the *Status* field of the JSON object.  A status of
  "complete" or "failed" indicates the job has finished running.  The returned
  JSON object roughly has the following schema:

```json
{
    "Id": "b1cd52ea474d4f58849082b54b16914c",
    "Cmd": [
        "[command]",
        "[arg1]",
        "[arg2]",
        "[...]",
    ],
    "Infiles": [
        {
            "Name": "input.xml",
            "Data": "base64 encoded string of file contents",
            "Cache": false
        }
    ],
    "Outfiles": [
        {
            "Name": "cyclus.sqlite",
            "Data": "base64 encoded string of file contents",
            "Cache": false
        }
    ],
    "Status": "complete",
    "Stdout": "standard output from the job process",
    "Stderr": "standard error from the job process",
    "Timeout": 600000000000,
    "Submitted": "2014-09-30T22:59:54.061622259-05:00",
    "Started": "2014-09-30T23:00:02.743536714-05:00",
    "Finished": "2014-09-30T23:00:09.029352256-05:00",
    "WorkerId": "024b7ff3f85047dcba19abbf011ebd53",
    "Note": ""
}
```

* GET to `[host]/api/v1/job-stat/[job-id]` returns a JSON object in the
  response body with information about the job status.
  output files for the job in the response body.  The returned JSON object has
  the following schema:

```json
{
    "Id": "b1cd52ea474d4f58849082b54b16914c",
    "Cmd": [
        "[command]",
        "[arg1]",
        "[arg2]",
        "[...]",
    ],
    "Size": 123456,
    "Status": "complete",
    "Stdout": "standard output from the job process",
    "Stderr": "standard error from the job process",
    "Submitted": "2014-09-30T22:59:54.061622259-05:00",
    "Started": "2014-09-30T23:00:02.743536714-05:00",
    "Finished": "2014-09-30T23:00:09.029352256-05:00",
}
```

  `Size` represents the size of the completed job in bytes including all input
  files, output files, stderr, and stdout.

* GET to `[host]/api/v1/job-outfiles/[job-id]` returns a zip-file of the
  output files for the job in the response body.

* POST to `[host]/api/v1/job-infile` creates a new default cyclus simulation
  job.  The request body is the raw bytes of the simulation input file. The
  *Location* field in the response header contains the URL endpoint where the
  created job status can be retrieved.  The response body contains a JSON
  object representing the created job.

* POST to `[host]/api/v1/job` submits a new job to be run.  The job must be
  specified as a JSON object present in the request body.  The job format is:

```json
{
    "Id": "[hex-encoded-random-uuid]",
    "Cmd": [
        "[command]",
        "[arg1]",
        "[arg2]",
        "[...]",
    ],
    "Infiles": [
        {
            "Name": "input.xml",
            "Data": "base64 encoded string of file contents"
        }
    ],
    "Outfiles": [
        {
            "Name": "cyclus.sqlite"
        }
    ],
    "Note": "extra notes about this job"
}
```

 The *Location* field in the response header contains the URL endpoint where
 the submitted job status can be retrieved.  The response body contains a JSON
 object representing the submitted job.

 For example, to just run a command and retrieve standard out, post a request
 to this endpoint with a JSON body like this:

```json
{
    "Id": "[hex-encoded-random-uuid]",
    "Cmd": [
        "cyclus",
        "-a"
    ]
}
```

 Send a GET request to `/api/v1/job` and use the text from the `Stdout` field
 of the JSON in the response body.

Updating the Cloudlus Server's Cyclus Instance
----------------------------------------------

Make sure the server has the appropriate tools

```bash
sudo apt-get install git docker
```

Get a copy of cloudlus

```bash
git clone https://github.com/rwcarlsen/cloudlus.git cloudlus-repo
```

Build the Docker image

```bash
cd cloudlus-repo
docker build -t cyclus/tip .
```

Kill previous workers

```bash
ps -ef | grep 'cloudlus.*work' | awk '{ print $2 }' | xargs kill
```

Run a couple of docker containers

```bash
for i in $(seq 10); do docker run -d cyclus/tip; done
```

