// Package rules embeds the default billing rules. Override at runtime with --rules.
package rules

import _ "embed"

// YAML is the default rules.yaml.
//
//go:embed rules.yaml
var YAML []byte
