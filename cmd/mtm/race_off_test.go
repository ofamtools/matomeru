//go:build !race

package main

// raceSlowdown scales the deadlines of tests that start a daemon.
const raceSlowdown = 1
