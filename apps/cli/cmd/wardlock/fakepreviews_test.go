// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"wardlock.nz/apiclient"
)

// The fake API's previews (C1.19).

var previewNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,18}[a-z0-9])?$`)

const (
	fakeDeployedAt = int64(1_800_000_060_000)
	fakeDay        = int64(24 * 60 * 60 * 1000)
)

// setPreview makes d, a preview deploy, its preview's deploy.
func (f *fakeAPI) setPreview(s *fakeSite, d *fakeDeploy) *apiclient.Preview {
	name := *d.Preview
	d.Status = apiclient.DeployStatusPreview
	if s.previews == nil {
		s.previews = map[string]*apiclient.Preview{}
	}
	p := s.previews[name]
	if p == nil {
		host := s.site.Name + "--" + name + "." + fakeBase
		p = &apiclient.Preview{Name: name, Hostname: host, Url: "https://" + host + "/", CreatedAt: fakeDeployedAt,
			ExpiresAt: ptr(fakeDeployedAt + 14*fakeDay)}
		s.previews[name] = p
	}
	switch e := deref(d.expires); {
	case e == "never":
		p.ExpiresAt = nil
	case e != "":
		days, _ := strconv.Atoi(strings.TrimSuffix(e, "d"))
		p.ExpiresAt = ptr(fakeDeployedAt + int64(days)*fakeDay)
	}
	p.DeployedAt, p.Deploy = fakeDeployedAt, d.Deploy
	return p
}

func (f *fakeAPI) handlePreviews(handle func(pattern, scope string, h func(w http.ResponseWriter, r *http.Request))) {
	handle("GET /api/v0/sites/{site}/previews", "", f.withSite(func(w http.ResponseWriter, r *http.Request, s *fakeSite) {
		var names []string
		for n := range s.previews {
			names = append(names, n)
		}
		slices.Sort(names)
		all := []apiclient.Preview{}
		for _, n := range names {
			all = append(all, *s.previews[n])
		}
		items, next := paginate(r, all)
		reply(w, http.StatusOK, apiclient.PreviewPage{Items: items, NextCursor: next})
	}))
	handle("DELETE /api/v0/sites/{site}/previews/{preview}", "deploy", f.withSite(func(w http.ResponseWriter, r *http.Request, s *fakeSite) {
		if s.previews[r.PathValue("preview")] == nil {
			reply(w, http.StatusNotFound, notFound)
			return
		}
		delete(s.previews, r.PathValue("preview"))
		w.WriteHeader(http.StatusNoContent)
	}))
	handle("POST /api/v0/sites/{site}/previews/{preview}/promote", "deploy", f.withSite(func(w http.ResponseWriter, r *http.Request, s *fakeSite) {
		p := s.previews[r.PathValue("preview")]
		if p == nil {
			reply(w, http.StatusNotFound, notFound)
			return
		}
		d := s.deployOf(p.Deploy.Id)
		if d.Path != nil && d.base != deref(s.site.ActiveDeployId) {
			reply(w, http.StatusConflict, apiErr("stale_base", "Another deploy went live on this site after this path deploy was created."))
			return
		}
		f.setActive(s, d)
		p.Deploy = d.Deploy
		reply(w, http.StatusOK, apiclient.Activation{Site: s.site, Deploy: d.Deploy, Preview: p})
	}))
}
