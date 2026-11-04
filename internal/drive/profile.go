package drive

import "net/http"

// ClientProfile holds the application identity sent with requests and the
// inputs used to compute captcha_sign.
type ClientProfile struct {
	ClientID          string
	ClientSecret      string
	PackageName       string
	ClientVersion     string
	ClientVersionCode string
	UserAgent         string
	// CaptchaSalts is the ordered salt cascade used to compute captcha_sign
	// with this profile's identity fields.
	CaptchaSalts []string
}

// Valid reports whether the profile can sign requests.
func (p ClientProfile) Valid() bool {
	return p.ClientID != "" && p.PackageName != "" && p.ClientVersion != "" && len(p.CaptchaSalts) > 0
}

// setHeaders attaches the identity every request to any host carries: the
// drive API, the auth exchanges and the captcha handshake all key risk
// control on the same set, so it is assembled here and nowhere else.
func (p ClientProfile) setHeaders(h http.Header, deviceID string) {
	h.Set("Accept", "application/json")
	h.Set("X-Client-Id", p.ClientID)
	h.Set("X-Device-Id", deviceID)
	if p.ClientVersion != "" {
		h.Set("X-Client-Version", p.ClientVersion)
	}
	if p.ClientVersionCode != "" {
		h.Set("X-Client-Version-Code", p.ClientVersionCode)
	}
	if p.UserAgent != "" {
		h.Set("User-Agent", p.UserAgent)
	}
}

// AndroidProfile uses identity parameters and captcha signing constants
// associated with the official Android client. The identity fields, ordered
// salts and signing algorithm match this public comparison snapshot:
// https://github.com/52funny/pikpakapi/tree/d105d48371a0df747673e44c00cec170984c9f6b
// ClientVersionCode is not verified by that comparison. These matches do not
// establish this project's original provenance or permission to redistribute
// third-party code. See THIRD_PARTY.md for the comparison and its limits.
// Using these values does not imply PikPak authorization or affiliation.
func AndroidProfile() ClientProfile {
	return ClientProfile{
		ClientID:          "YNxT9w7GMdWvEOKa",
		ClientSecret:      "dbw2OtmVEeuUvIptb1Coyg",
		PackageName:       "com.pikcloud.pikpak",
		ClientVersion:     "1.21.0",
		ClientVersionCode: "10083",
		UserAgent:         "ANDROID-com.pikcloud.pikpak/1.21.0",
		CaptchaSalts: []string{
			"",
			"E32cSkYXC2bciKJGxRsE8ZgwmH/YwkvpD6/O9guSOa2irCwciH4xPHaH",
			"QtqgfMgHP2TFl",
			"zOKgHT56L7nIzFzDpUGhpWFrgP53m3G6ML",
			"S",
			"THxpsktzfFXizUv7DK1y/N7NZ1WhayViluBEvAJJ8bA1Wr6",
			"y9PXH3xGUhG/zQI8CaapRw2LhldCaFM9CRlKpZXJvj+pifu",
			"+RaaG7T8FRTI4cP019N5y9ofLyHE9ySFUr",
			"6Pf1l8UTeuzYldGtb/d",
		},
	}
}

// WebClientID identifies the public client used by PikPak's web QR login.
const WebClientID = "YUMx5nI8ZU8Ap8pm"

// WebProfile uses the web drive's captcha identity, without an Android secret.
// Source: https://mypikpak.com/drive/3877.fc93f11b.js (build 89b307c6).
func WebProfile() ClientProfile {
	return ClientProfile{
		ClientID:      WebClientID,
		PackageName:   "mypikpak.com",
		ClientVersion: "2.0.0",
		CaptchaSalts: []string{
			"C9qPpZLN8ucRTaTiUMWYS9cQvWOE",
			"+r6CQVxjzJV6LCV",
			"F",
			"pFJRC",
			"9WXYIDGrwTCz2OiVlgZa90qpECPD6olt",
			"/750aCr4lm/Sly/c",
			"RB+DT/gZCrbV",
			"",
			"CyLsf7hdkIRxRm215hl",
			"7xHvLi2tOYP0Y92b",
			"ZGTXXxu8E/MIWaEDB+Sm/",
			"1UI3",
			"E7fP5Pfijd+7K+t6Tg/NhuLq0eEUVChpJSkrKxpO",
			"ihtqpG6FMt65+Xk+tWUH2",
			"NhXXU9rg4XXdzo7u5o",
		},
	}
}
