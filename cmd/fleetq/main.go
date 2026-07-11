// Command fleetq is both the server and its client.
//
//	fleetq serve [--queue memory|sqlite|postgres] [--addr :8080] [--fleet DIR]
//	  Start the fleet. It comes up with ZERO clusters unless --fleet preloads a
//	  JGF directory. Register clusters and submit jobs against it via the API.
//
//	fleetq cluster register --name c1 --manager flux-operator --nodes 4:64 --caps lammps,efa
//	fleetq cluster list
//	fleetq cluster unregister c1
//	fleetq submit --name job1 --image img --command "lmp -i in.reaxff" --nodes 4 --tasks-per-node 64 --caps lammps,efa
//	fleetq jobs
//	fleetq job <id>
//	fleetq log <id>
//
// The client commands talk to a running server (--server, default
// http://localhost:8080).
package main

import (
	"fmt"
	"os"
)

func usage() {
	fmt.Fprint(os.Stderr, `fleetq — fleet-level dispatch queue

server:
  fleetq serve [--queue memory|sqlite|postgres] [--dsn DSN] [--addr :8080] [--fleet DIR]

clusters (edits need --secret / $FLEETQ_SECRET):
  fleetq managers                                                # supported managers + real/emulated dispatch
  fleetq cluster register --name N --manager M [--handle H]      # prints a secret
  fleetq cluster list
  fleetq cluster unregister --name N
  fleetq cluster subsystem register   --cluster C --file g.json [--name S] [--descriptive=false]
  fleetq cluster subsystem unregister --cluster C --name S

jobs (content is a Flux jobspec file):
  fleetq submit  --file job.json
  fleetq satisfy --file job.json       # dry-run: ranked feasible clusters, allocates nothing
  fleetq jobs
  fleetq job <id>
  fleetq log <id>

Client commands accept --server (default http://localhost:8080).
`)
}

func main() {
	// If invoked as a Fluxion worker subprocess, serve reapi and exit (fluxion build only).
	maybeFluxionWorker()
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = runServe(os.Args[2:])
	case "cluster":
		err = runCluster(os.Args[2:])
	case "managers":
		err = runManagers(os.Args[2:])
	case "submit":
		err = runSubmit(os.Args[2:])
	case "satisfy":
		err = runSatisfy(os.Args[2:])
	case "jobs":
		err = runJobs(os.Args[2:])
	case "job":
		err = runJob(os.Args[2:])
	case "log":
		err = runLog(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
