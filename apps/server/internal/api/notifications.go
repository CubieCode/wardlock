// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"

	"wardlock.nz/server/internal/notify"
)

func notificationSettingsBody(on map[string]bool) NotificationSettings {
	return NotificationSettings{
		DomainVerified:  on[notify.DomainVerified],
		DomainFailing:   on[notify.DomainFailing],
		LinkUsedUp:      on[notify.LinkUsedUp],
		LinksExpiring:   on[notify.LinksExpiring],
		TokenExpiring:   on[notify.TokenExpiring],
		PreviewExpiring: on[notify.PreviewExpiring],
	}
}

// GetNotificationSettings returns the session owner's settings (I6.2).
// API tokens never reach this: requireTokenScopes refuses them.
func (s *Server) GetNotificationSettings(ctx context.Context, _ GetNotificationSettingsRequestObject) (GetNotificationSettingsResponseObject, error) {
	ownerID, ok := sessionOwner(ctx)
	if !ok {
		return GetNotificationSettings401JSONResponse{UnauthenticatedJSONResponse(unauthenticatedError())}, nil
	}
	on, err := s.Notifications.Settings(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	return GetNotificationSettings200JSONResponse(notificationSettingsBody(on)), nil
}

// SetNotificationSettings replaces the session owner's settings (I6.2).
func (s *Server) SetNotificationSettings(ctx context.Context, req SetNotificationSettingsRequestObject) (SetNotificationSettingsResponseObject, error) {
	ownerID, ok := sessionOwner(ctx)
	if !ok {
		return SetNotificationSettings401JSONResponse{UnauthenticatedJSONResponse(unauthenticatedError())}, nil
	}
	if req.Body == nil {
		return SetNotificationSettings400JSONResponse{BadRequestJSONResponse(invalidField("body", "A body is required."))}, nil
	}
	changes := map[string]bool{}
	for event, value := range map[string]*bool{
		notify.DomainVerified:  req.Body.DomainVerified,
		notify.DomainFailing:   req.Body.DomainFailing,
		notify.LinkUsedUp:      req.Body.LinkUsedUp,
		notify.LinksExpiring:   req.Body.LinksExpiring,
		notify.TokenExpiring:   req.Body.TokenExpiring,
		notify.PreviewExpiring: req.Body.PreviewExpiring,
	} {
		if value != nil {
			changes[event] = *value
		}
	}
	if err := s.Notifications.SetSettings(ctx, ownerID, changes); err != nil {
		return nil, err
	}
	on, err := s.Notifications.Settings(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	return SetNotificationSettings200JSONResponse(notificationSettingsBody(on)), nil
}
