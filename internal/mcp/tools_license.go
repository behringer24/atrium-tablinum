package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/behringer24/atrium-tablinum/internal/license"
	"github.com/behringer24/atrium-tablinum/internal/product"
)

type licenseIn struct {
	Action  string `json:"action" jsonschema:"get, install or remove"`
	License string `json:"license,omitempty" jsonschema:"install: the licence text, one line starting with atrium-license-v1."`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"install and remove: true only after confirmation by the user"`
}

// licenseView is the licence as shown to an instance admin.
type licenseView struct {
	Status      string           `json:"status"` // none, valid, not covered, expired, wrong domain, invalid
	Edition     string           `json:"edition"`
	Detail      string           `json:"detail"`
	Source      string           `json:"source,omitempty"` // installed (by an admin) or file
	Licensee    string           `json:"licensee,omitempty"`
	LicenseID   string           `json:"licence_id,omitempty"`
	Domain      string           `json:"domain,omitempty"`
	Features    []string         `json:"features,omitempty"`
	Limits      map[string]int64 `json:"limits,omitempty"` // 0 = unlimited
	Issued      string           `json:"issued,omitempty"`
	Maintenance string           `json:"maintenance_until,omitempty"`
	Expires     string           `json:"expires,omitempty"`
}

type licenseOut struct {
	Message string      `json:"message,omitempty"`
	License licenseView `json:"license"`
	// Build says what the running build is: the release line, its date (the date compared with
	// maintenance_until), and the commit date for support.
	Build struct {
		Version   string `json:"version"`
		Line      string `json:"line"`
		LineDate  string `json:"line_date"`
		BuildDate string `json:"build_date,omitempty"`
	} `json:"build"`
	// CommunityMaxUsers is the user limit without a licence that sets one (0 = unlimited).
	CommunityMaxUsers int `json:"community_max_users"`
}

func (s *Server) licenseOut(msg string) licenseOut {
	st := s.License.State()
	out := licenseOut{Message: msg, CommunityMaxUsers: s.License.CommunityMaxUsers()}
	out.License = licenseView{Status: string(st.Status), Edition: st.Edition, Detail: st.Detail, Source: s.License.Source()}
	if c := st.Claims; c != nil {
		out.License.Licensee, out.License.LicenseID, out.License.Domain = c.Licensee, c.ID, c.Domain
		out.License.Features, out.License.Limits = c.Features, c.Limits
		out.License.Issued, out.License.Maintenance, out.License.Expires = c.Issued, c.MaintenanceUntil, c.Expires
	}
	line := product.CurrentLine()
	out.Build.Version, out.Build.Line, out.Build.LineDate, out.Build.BuildDate = product.Version, line.Name, line.Coverage().Format("2006-01-02"), product.BuildDate
	return out
}

func (s *Server) adminLicense(ctx context.Context, c *call, in licenseIn) (licenseOut, error) {
	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "get":
		return s.licenseOut(""), nil
	case "install":
		if strings.TrimSpace(in.License) == "" {
			return licenseOut{}, userErr("Give the licence text (a line starting with atrium-license-v1.).")
		}
		if !in.Confirm {
			return licenseOut{}, userErr("Installing a licence changes limits and features at once. Tell the user what it contains and ask for confirmation, then call again with confirm=true.")
		}
		st, err := s.License.Install(ctx, in.License)
		switch {
		case errors.Is(err, license.ErrFormat), errors.Is(err, license.ErrUnknownKey), errors.Is(err, license.ErrSignature):
			return licenseOut{}, userErr("This licence is not accepted: " + err.Error() + ".")
		case err != nil:
			return licenseOut{}, userErr("This licence is not accepted: " + err.Error())
		}
		details := map[string]any{"status": string(st.Status)}
		if st.Claims != nil {
			details["licence_id"], details["licensee"], details["edition"], details["maintenance_until"] =
				st.Claims.ID, st.Claims.Licensee, st.Claims.Edition, st.Claims.MaintenanceUntil
		}
		s.record(ctx, c.Actor, "license_install", nil, details)
		return s.licenseOut(fmt.Sprintf("The licence is installed. %s", st.Detail)), nil
	case "remove":
		if !in.Confirm {
			return licenseOut{}, userErr("Removing the licence returns the instance to the Community edition (or to the licence in the configuration file). Ask the user, then call again with confirm=true.")
		}
		if err := s.License.Remove(ctx); err != nil {
			return licenseOut{}, err
		}
		s.record(ctx, c.Actor, "license_remove", nil, map[string]any{})
		return s.licenseOut("The installed licence is removed."), nil
	}
	return licenseOut{}, userErr("action must be get, install or remove.")
}
