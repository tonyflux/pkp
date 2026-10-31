//go:build production

package main

// releaseBuild selects the production statistics endpoint and update checks.
// Statistics still require opt-in. Development builds use PKP_TELEMETRY_API if set.
const releaseBuild = true
