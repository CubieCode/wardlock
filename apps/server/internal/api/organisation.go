// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"net/http"

	"wardlock.nz/server/internal/sites"
	"wardlock.nz/server/internal/store"
)

// Groups, tags and pins (V5.3). Every handler takes the owner from the
// verified request context; a missing or another owner's group, tag or
// site answers the same 404.

func groupBody(g store.Group) Group {
	out := Group{Id: g.ID, Name: g.Name, CreatedAt: g.CreatedAt.UnixMilli()}
	if g.Note != "" {
		out.Note = &g.Note
	}
	return out
}

func tagBody(t store.Tag) Tag {
	out := Tag{Id: t.ID, Name: t.Name, CreatedAt: t.CreatedAt.UnixMilli()}
	if t.Colour > 0 && t.Colour <= sites.MaxTagColour { // the schema keeps it 0-8
		out.Colour = TagColour(t.Colour)
	}
	return out
}

// siteBodyWithTags is siteBody with the site's tags looked up.
func (s *Server) siteBodyWithTags(ctx context.Context, owner string, site store.Site) (Site, error) {
	tags, err := s.Sites.TagIDs(ctx, owner, site)
	if err != nil {
		return Site{}, err
	}
	return s.siteBody(site, tags[site.ID]), nil
}

var missingBody = fail(http.StatusBadRequest, "invalid_request", "The request body is required.", "")

// ListGroups lists the owner's groups by name.
func (s *Server) ListGroups(ctx context.Context, _ ListGroupsRequestObject) (ListGroupsResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return ListGroupsdefaultJSONResponse(*unauthenticated), nil
	}
	groups, err := s.Sites.ListGroups(ctx, owner)
	if err != nil {
		return nil, err
	}
	out := ListGroups200JSONResponse{Items: make([]Group, len(groups))}
	for i, g := range groups {
		out.Items[i] = groupBody(g)
	}
	return out, nil
}

// CreateGroup creates a group.
func (s *Server) CreateGroup(ctx context.Context, req CreateGroupRequestObject) (CreateGroupResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return CreateGroupdefaultJSONResponse(*unauthenticated), nil
	}
	if req.Body == nil {
		return CreateGroupdefaultJSONResponse(*missingBody), nil
	}
	g, err := s.Sites.CreateGroup(ctx, owner, req.Body.Name, deref(req.Body.Note))
	if err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return CreateGroupdefaultJSONResponse(*f), nil
	}
	return CreateGroup201JSONResponse(groupBody(g)), nil
}

// UpdateGroup renames a group or changes its note.
func (s *Server) UpdateGroup(ctx context.Context, req UpdateGroupRequestObject) (UpdateGroupResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return UpdateGroupdefaultJSONResponse(*unauthenticated), nil
	}
	if req.Body == nil {
		return UpdateGroupdefaultJSONResponse(*missingBody), nil
	}
	g, err := s.Sites.UpdateGroup(ctx, owner, req.GroupId, req.Body.Name, req.Body.Note)
	if err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return UpdateGroupdefaultJSONResponse(*f), nil
	}
	return UpdateGroup200JSONResponse(groupBody(g)), nil
}

// DeleteGroup deletes a group; its sites become ungrouped.
func (s *Server) DeleteGroup(ctx context.Context, req DeleteGroupRequestObject) (DeleteGroupResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return DeleteGroupdefaultJSONResponse(*unauthenticated), nil
	}
	if err := s.Sites.DeleteGroup(ctx, owner, req.GroupId); err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return DeleteGroupdefaultJSONResponse(*f), nil
	}
	return DeleteGroup204Response{}, nil
}

// ListTags lists the owner's tags by name.
func (s *Server) ListTags(ctx context.Context, _ ListTagsRequestObject) (ListTagsResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return ListTagsdefaultJSONResponse(*unauthenticated), nil
	}
	tags, err := s.Sites.ListTags(ctx, owner)
	if err != nil {
		return nil, err
	}
	out := ListTags200JSONResponse{Items: make([]Tag, len(tags))}
	for i, t := range tags {
		out.Items[i] = tagBody(t)
	}
	return out, nil
}

// CreateTag creates a tag.
func (s *Server) CreateTag(ctx context.Context, req CreateTagRequestObject) (CreateTagResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return CreateTagdefaultJSONResponse(*unauthenticated), nil
	}
	if req.Body == nil {
		return CreateTagdefaultJSONResponse(*missingBody), nil
	}
	t, err := s.Sites.CreateTag(ctx, owner, req.Body.Name, colour(req.Body.Colour))
	if err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return CreateTagdefaultJSONResponse(*f), nil
	}
	return CreateTag201JSONResponse(tagBody(t)), nil
}

// UpdateTag renames a tag or changes its colour.
func (s *Server) UpdateTag(ctx context.Context, req UpdateTagRequestObject) (UpdateTagResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return UpdateTagdefaultJSONResponse(*unauthenticated), nil
	}
	if req.Body == nil {
		return UpdateTagdefaultJSONResponse(*missingBody), nil
	}
	t, err := s.Sites.UpdateTag(ctx, owner, req.TagId, req.Body.Name, colour(req.Body.Colour))
	if err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return UpdateTagdefaultJSONResponse(*f), nil
	}
	return UpdateTag200JSONResponse(tagBody(t)), nil
}

// DeleteTag deletes a tag and removes it from its sites.
func (s *Server) DeleteTag(ctx context.Context, req DeleteTagRequestObject) (DeleteTagResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return DeleteTagdefaultJSONResponse(*unauthenticated), nil
	}
	if err := s.Sites.DeleteTag(ctx, owner, req.TagId); err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return DeleteTagdefaultJSONResponse(*f), nil
	}
	return DeleteTag204Response{}, nil
}

// SetSiteOrganisation sets one site's group and tags. A deploy-scoped
// token may call it (V5.15); it never creates a group or tag.
func (s *Server) SetSiteOrganisation(ctx context.Context, req SetSiteOrganisationRequestObject) (SetSiteOrganisationResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return SetSiteOrganisationdefaultJSONResponse(*unauthenticated), nil
	}
	if req.Body == nil {
		return SetSiteOrganisationdefaultJSONResponse(*missingBody), nil
	}
	if req.Body.TagIds == nil {
		return SetSiteOrganisationdefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "tagIds is required; send [] for no tags.", "tagIds")), nil
	}
	site, err := s.Sites.SetOrganisation(ctx, owner, req.Site, deref(req.Body.GroupId), req.Body.TagIds)
	if err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return SetSiteOrganisationdefaultJSONResponse(*f), nil
	}
	body, err := s.siteBodyWithTags(ctx, owner, site)
	if err != nil {
		return nil, err
	}
	return SetSiteOrganisation200JSONResponse(body), nil
}

// PinSite pins a site.
func (s *Server) PinSite(ctx context.Context, req PinSiteRequestObject) (PinSiteResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return PinSitedefaultJSONResponse(*unauthenticated), nil
	}
	body, f, err := s.setPinned(ctx, owner, req.Site, true)
	if f != nil || err != nil {
		if f == nil {
			return nil, err
		}
		return PinSitedefaultJSONResponse(*f), nil
	}
	return PinSite200JSONResponse(body), nil
}

// UnpinSite unpins a site.
func (s *Server) UnpinSite(ctx context.Context, req UnpinSiteRequestObject) (UnpinSiteResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return UnpinSitedefaultJSONResponse(*unauthenticated), nil
	}
	body, f, err := s.setPinned(ctx, owner, req.Site, false)
	if f != nil || err != nil {
		if f == nil {
			return nil, err
		}
		return UnpinSitedefaultJSONResponse(*f), nil
	}
	return UnpinSite200JSONResponse(body), nil
}

func (s *Server) setPinned(ctx context.Context, owner, name string, pinned bool) (Site, *failure, error) {
	site, err := s.Sites.SetPinned(ctx, owner, name, pinned)
	if err != nil {
		f, err := failed(err)
		return Site{}, f, err
	}
	body, err := s.siteBodyWithTags(ctx, owner, site)
	return body, nil, err
}

// OrganiseSites changes many sites at once, all or nothing, and answers
// their organisation from before.
func (s *Server) OrganiseSites(ctx context.Context, req OrganiseSitesRequestObject) (OrganiseSitesResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return OrganiseSitesdefaultJSONResponse(*unauthenticated), nil
	}
	if req.Body == nil {
		return OrganiseSitesdefaultJSONResponse(*missingBody), nil
	}
	body := req.Body
	if body.GroupId != nil && deref(body.Ungroup) {
		return OrganiseSitesdefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "Send groupId or ungroup, not both.", "ungroup")), nil
	}
	b := sitesBulk(body)
	prior, err := s.Sites.Organise(ctx, owner, body.Sites, b)
	if err != nil {
		f, err := failed(err)
		if f == nil {
			return nil, err
		}
		return OrganiseSitesdefaultJSONResponse(*f), nil
	}
	out := OrganiseSites200JSONResponse{Prior: make([]SiteOrganisation, len(prior))}
	for i, p := range prior {
		o := SiteOrganisation{Site: p.SiteName, TagIds: p.TagIDs, Pinned: !p.PinnedAt.IsZero()}
		if p.GroupID != "" {
			o.GroupId = &p.GroupID
		}
		out.Prior[i] = o
	}
	return out, nil
}

func sitesBulk(body *OrganiseSitesRequest) sites.Bulk {
	b := sites.Bulk{Pinned: body.Pinned}
	if body.AddTagIds != nil {
		b.AddTagIDs = *body.AddTagIds
	}
	if body.RemoveTagIds != nil {
		b.RemoveTagIDs = *body.RemoveTagIds
	}
	if body.GroupId != nil {
		b.SetGroup, b.GroupID = true, *body.GroupId
	}
	if deref(body.Ungroup) {
		b.SetGroup = true
	}
	return b
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func colour(c *TagColour) *int {
	if c == nil {
		return nil
	}
	v := int(*c)
	return &v
}
