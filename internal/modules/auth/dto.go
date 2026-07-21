package auth

import (
	"time"

	"github.com/runix/runix/internal/modules/users"
)

type loginRequest struct {
	Identifier string `json:"identifier" binding:"required,max=256"`
	Password   string `json:"password" binding:"required,max=512"`
	Remember   bool   `json:"remember"`
}

type loginResponse struct {
	MFARequired bool          `json:"mfaRequired"`
	MFAToken    string        `json:"mfaToken,omitempty"`
	Tokens      *TokenPair    `json:"tokens,omitempty"`
	User        *users.Public `json:"user,omitempty"`
}

type mfaVerifyRequest struct {
	MFAToken string `json:"mfaToken" binding:"required"`
	Code     string `json:"code" binding:"required,max=32"`
	Remember bool   `json:"remember"`
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken" binding:"required"`
}

type logoutRequest struct {
	RefreshToken string `json:"refreshToken" binding:"required"`
}

type mfaEnableRequest struct {
	Code string `json:"code" binding:"required,max=32"`
}

type mfaDisableRequest struct {
	Password string `json:"password" binding:"required"`
	Code     string `json:"code" binding:"required,max=32"`
}

type recoveryCodesResponse struct {
	RecoveryCodes []string `json:"recoveryCodes"`
}

type createPATRequest struct {
	Name      string     `json:"name" binding:"required,max=128"`
	ExpiresAt *time.Time `json:"expiresAt"`
}

type meResponse struct {
	User        users.Public `json:"user"`
	Permissions []string     `json:"permissions"`
	Roles       []string     `json:"roles"`
}
