// Package platform holds the small amount of plumbing every service shares:
// configuration, logging, metrics and HTTP serving.
package platform

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Env reads typed settings from environment variables. It collects every
// problem instead of stopping at the first, so a misconfigured service reports
// all of its bad settings in one log line.
type Env struct{ errs []string }

func (e *Env) String(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func (e *Env) Required(key string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		e.errs = append(e.errs, key+" is required")
	}
	return v
}

func (e *Env) Int(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Sprintf("%s: %q is not an integer", key, v))
		return def
	}
	return n
}

func (e *Env) Duration(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Sprintf("%s: %q is not a duration", key, v))
		return def
	}
	return d
}

// List splits a comma-separated value, dropping empty entries.
func (e *Env) List(key, def string) []string {
	var out []string
	for _, part := range strings.Split(e.String(key, def), ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Check records a validation failure when ok is false.
func (e *Env) Check(ok bool, msg string) {
	if !ok {
		e.errs = append(e.errs, msg)
	}
}

func (e *Env) Err() error {
	if len(e.errs) == 0 {
		return nil
	}
	return fmt.Errorf("invalid configuration: %s", strings.Join(e.errs, "; "))
}
