package models

import "github.com/coreos/go-oidc"

type LoginResponse struct {
	IDToken                  oidc.IDToken `json:"idToken"`
	AccessToken              string       `json:"accessToken"`
	RefreshToken             string       `json:"refreshToken"`
	Email                    string       `json:"email"`
	CertificateAuthorityData string       `json:"certificateAuthorityData"`
	ServerBaseURL            string       `json:"serverBaseUrl"`
	ExpiresAt                int          `json:"expiresAt"`
}
