//go:build fluxcore

package main

import "github.com/converged-computing/fleetq/pkg/cluster"

// realFluxDriver returns the libflux dispatch driver (built with -tags fluxcore).
func realFluxDriver() cluster.Driver { return cluster.NewFluxCGODriver() }
