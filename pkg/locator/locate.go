package locator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/pkg/errors"
)

const DefaultHost = "isle.dev"

type Location struct {
	Registry string
	Name     string
}

type ResolvedLocation struct {
	Type     string
	Location string
}

var repl = strings.NewReplacer("/", "--")

func (r *ResolvedLocation) Name() string {
	return r.Type + "-" + repl.Replace(r.Location)
}

// Resolve will translate a short name into a resolved name.
// This includes resolving any vanity/well-known redirects.
func Resolve(ctx context.Context, ref string) (*Location, error) {
	slash := strings.IndexByte(ref, '/')

	var host, name string

	if slash == -1 {
		host = DefaultHost
		name = ref
	} else {
		host = ref[:slash]
		name = ref[slash+1:]
	}

	loc := &Location{
		Registry: host,
		Name:     name,
	}

	return loc, nil
}

type WellKnownApp struct {
	Type   string `json:"type,omitempty"`
	Target string `json:"target,omitempty"`
	GitHub string `json:"github,omitempty"`
	OCI    string `json:"oci,omitempty"`
}

func (wk *WellKnownApp) MakeCanonical() {
	if wk.GitHub != "" {
		wk.Type = "github"
		wk.Target = wk.GitHub
	}

	if wk.OCI != "" {
		wk.Type = "oci"
		wk.Target = wk.OCI
	}
}

type WellKnown struct {
	Apps map[string]*WellKnownApp `json:"apps"`
}

func (wk *WellKnown) MakeCanonical() {
	for _, app := range wk.Apps {
		app.MakeCanonical()
	}
}

var (
	ErrUnknownLocation = errors.New("unknown location")
)

func ResolveLocation(ctx context.Context, loc *Location) (*ResolvedLocation, error) {
	scheme := "https"
	if loc.Registry == "localhost" {
		scheme = "http"
	}
	url := fmt.Sprintf("%s://%s/.well-known/isle.json", scheme, loc.Registry)

	resp, err := http.Get(url)
	if err == nil && resp.StatusCode == 200 {
		var wk WellKnown
		err = json.NewDecoder(resp.Body).Decode(&wk)
		if err == nil {
			wk.MakeCanonical()

			return ResolveFromWellKnown(loc, &wk)
		}
	}

	return nil, errors.Wrapf(ErrUnknownLocation, "unknown registry type: %s", loc.Registry)
}

func MatchSelector(selector, name string) bool {
	switch selector {
	case "*", name:
		return true
	}

	re, err := regexp.Compile(selector)
	if err == nil {
		return re.MatchString(name)
	}

	return false
}

func ResolveFromWellKnown(loc *Location, wk *WellKnown) (*ResolvedLocation, error) {
	for selector, app := range wk.Apps {
		if MatchSelector(selector, loc.Name) {
			return &ResolvedLocation{
				Type:     app.Type,
				Location: app.Target,
			}, nil
		}
	}

	return DetectLocation(loc)
}

func DetectLocation(loc *Location) (*ResolvedLocation, error) {
	if loc.Registry == "github.com" {
		return &ResolvedLocation{
			Type:     "github",
			Location: loc.Name,
		}, nil
	}

	return nil, errors.Wrapf(ErrUnknownLocation, "unknown registry: %s", loc.Registry)
}
