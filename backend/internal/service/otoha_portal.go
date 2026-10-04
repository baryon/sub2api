package service

import (
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// In the Otoha portal (config otoha.portal, TASK-61) regular users buy the service and the site creates and keeps
// their one "Otoha Desktop" key; they do not manage keys or pick groups. These are the answers the user API gives
// when they try; admins are not affected.
var (
	ErrOtohaPortalKeysManaged = infraerrors.Forbidden("OTOHA_PORTAL_KEYS_MANAGED",
		"On this site your key is created and kept for you, so keys cannot be added, changed or deleted here. Open My Otoha to connect the app")
	ErrOtohaPortalGroupsManaged = infraerrors.Forbidden("OTOHA_PORTAL_GROUPS_MANAGED",
		"On this site the models come with your plan, so groups and channels cannot be listed or chosen here")
)
