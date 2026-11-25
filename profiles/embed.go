// Package profiles holds the profiles compiled into lbf.
package profiles

import _ "embed"

//go:embed bronze/0.7.0/profile.json
var Bronze []byte

//go:embed bronze/0.7.0/frame.json
var BronzeFrame []byte
