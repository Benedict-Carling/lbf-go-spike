// Package profiles holds the profiles compiled into lbf.
package profiles

import _ "embed"

//go:embed bronze/profile.json
var Bronze []byte
