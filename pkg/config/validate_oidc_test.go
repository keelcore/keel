package config

func validOIDC() OIDCConfig {
	return OIDCConfig{
		Enabled: true, Issuer: "https://idp.example", ClientID: "keel",
		RedirectURL: "https://app.example/oauth2/callback", CookieKeyFile: "/etc/keel/cookie.key",
	}
}
