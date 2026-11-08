//go:build race

package main

// raceSlowdown scales the deadlines of tests that start a daemon: the race
// detector makes the daemon (a copy of the test binary) start and run slowly.
const raceSlowdown = 4
