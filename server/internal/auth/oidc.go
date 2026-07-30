package auth

import (
	"context"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// go-oidc discovers provider metadata and verifies signed ID tokens. oauth2
// performs the authorization-code exchange with the PKCE verifier.

type Identity struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
}

type OIDCProvider interface {
	AuthCodeURL(state, challenge string) string
	Exchange(context.Context, string, string) (Identity, error)
}

type Provider struct {
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
}

func NewProvider(ctx context.Context, issuer, clientID, clientSecret, redirectURL string) (*Provider, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	return &Provider{
		oauth: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  redirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: clientID}),
	}, nil
}

func (p *Provider) AuthCodeURL(state, challenge string) string {
	return p.oauth.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"))
}

func (p *Provider) Exchange(ctx context.Context, code, verifier string) (Identity, error) {
	token, err := p.oauth.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", verifier))
	if err != nil {
		return Identity{}, err
	}
	rawIDToken, _ := token.Extra("id_token").(string)
	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return Identity{}, err
	}
	var claims struct {
		Subject       string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return Identity{}, err
	}
	return Identity{
		Subject: claims.Subject, Email: claims.Email,
		EmailVerified: claims.EmailVerified, Name: claims.Name,
	}, nil
}
