package update

import (
	"crypto/ed25519"
	"encoding/base64"
)

// Only this public key ships in PKP. Keep the matching signing seed outside the
// source tree; changing this key requires a separately planned trust rotation.
const publicKey = "OWVabjpKxRDdGMlH9Jpk7NYhth7AFCws7Nvaox6C/ac="

func PublicKey() ed25519.PublicKey {
	key, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		panic("invalid embedded update public key")
	}
	return ed25519.PublicKey(key)
}
