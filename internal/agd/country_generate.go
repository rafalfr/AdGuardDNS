//go:build generate

package main

import (
	"encoding/csv"
	"net/http"
	"os"
	"text/template"
	"time"

	"github.com/AdguardTeam/AdGuardDNS/internal/agdhttp"
	"github.com/AdguardTeam/golibs/httphdr"
	"github.com/AdguardTeam/golibs/log"
	"golang.org/x/exp/slices"
)

func main() {
	c := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest(http.MethodGet, csvURL, nil)
	check(err)

	req.Header.Add(httphdr.UserAgent, agdhttp.UserAgent())

	resp, err := c.Do(req)
	check(err)
	defer log.OnCloserError(resp.Body, log.ERROR)

	out, err := os.OpenFile("./country.go", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o664)
	check(err)
	defer log.OnCloserError(out, log.ERROR)

	r := csv.NewReader(resp.Body)
	rows, err := r.ReadAll()
	check(err)

	// Skip the first row, as it is a header.
	rows = rows[1:]

	// Sort by the code to make the output more predictable and easier to look
	// through.
	slices.SortFunc(rows, func(a, b []string) (less bool) {
		return a[1] < b[1]
	})

	tmpl, err := template.New("main").Parse(tmplStr)
	check(err)

	err = tmpl.Execute(out, rows)
	check(err)
}

// csvURL is the default URL of the information about country codes.
const csvURL = `https://raw.githubusercontent.com/lukes/ISO-3166-Countries-with-Regional-Codes/master/slim-2/slim-2.csv`

// tmplStr is the template of the generated Go code.
const tmplStr = `// Code generated by go run ./country_generate.go; DO NOT EDIT.

package agd

import (
	"encoding"
	"fmt"

	"github.com/AdguardTeam/golibs/errors"
)

// Country Codes

// Country represents an ISO 3166-1 alpha-2 country code.
type Country string

// Country code constants.  Note that these constants don't include the
// user-assigned ones.
const (
	// CountryNone is an invalid or unknown country code.
	CountryNone Country = ""{{ range . }}
	{{ $name := (index . 0) -}}
	{{ $code := (index . 1) -}}
	// Country{{$code}} is the ISO 3166-1 alpha-2 code for
	// {{ $name }}.
	Country{{$code}} Country = {{ printf "%q" $code }}{{ end }}

	// CountryXK is the user-assigned ISO 3166-1 alpha-2 code for Republic of
	// Kosovo.  Kosovo does not have a recognized ISO 3166 code, but it is still
	// an entity whose user-assigned code is relatively common.
	CountryXK Country = "XK"
)

// NewCountry converts s into a Country while also validating it.  Prefer to use
// this instead of a plain conversion.
func NewCountry(s string) (c Country, err error) {
	c = Country(s)
	if isUserAssigned(s) {
		return c, nil
	}

	switch c {
	case
		{{ range . -}}
		{{ $code := (index . 1) -}}
		Country{{ $code }},
		{{ end -}}
		CountryNone:
		return c, nil
	default:
		return CountryNone, &NotACountryError{Code: s}
	}
}

// type check
var _ encoding.TextUnmarshaler = (*Country)(nil)

// UnmarshalText implements the encoding.TextUnmarshaler interface for *Country.
func (c *Country) UnmarshalText(b []byte) (err error) {
	if c == nil {
		return errors.Error("nil country")
	}

	ctry, err := NewCountry(string(b))
	if err != nil {
		return fmt.Errorf("decoding country: %w", err)
	}

	*c = ctry

	return nil
}

// isUserAssigned returns true if s is a user-assigned ISO 3166-1 alpha-2
// country code.
func isUserAssigned(s string) (ok bool) {
	if len(s) != 2 {
		return false
	}

	return s == "AA" || s == "OO" || s == "ZZ" || s[0] == 'X' || (s[0] == 'Q' && s[1] >= 'M')
}
`

// check is a simple error checker.
func check(err error) {
	if err != nil {
		panic(err)
	}
}
