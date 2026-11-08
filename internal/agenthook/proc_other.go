// Adapted from tuios (github.com/Gaurav-Gosain/tuios) v0.12.1, MIT License,
// Copyright (c) 2025 Gaurav Gosain. See LICENSE-tuios in this directory.

//go:build !linux && !darwin

package agenthook

// Other platforms have no session id a pane's shell leads and no portable
// parent read, so only the immediate parent is offered.
func selfSID() int { return 0 }

// processName has no portable source here, so every name is unknown and
// HarnessPID takes the nearest ancestor.
func processName(int) string { return "" }
