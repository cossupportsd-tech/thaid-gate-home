// Package oidc implements the parts of OpenID Connect needed to log in with
// ThaID: discovery, PKCE, the authorization code exchange and ES256 ID token
// verification. It uses only the Go standard library.
package oidc

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

var b64 = base64.RawURLEncoding

// JWK is one entry of a JSON Web Key Set. Only EC P-256 keys are used,
// because ThaID signs ID tokens with ES256.
type JWK struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// JWKS is a JSON Web Key Set document.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// PublicKey converts the JWK into an ECDSA key, rejecting points that are not
// on the P-256 curve.
func (k JWK) PublicKey() (*ecdsa.PublicKey, error) {
	if k.Kty != "EC" || k.Crv != "P-256" {
		return nil, fmt.Errorf("unsupported key type %s/%s", k.Kty, k.Crv)
	}
	x, err := b64.DecodeString(k.X)
	if err != nil || len(x) != 32 {
		return nil, errors.New("invalid x coordinate")
	}
	y, err := b64.DecodeString(k.Y)
	if err != nil || len(y) != 32 {
		return nil, errors.New("invalid y coordinate")
	}
	raw := append(append([]byte{4}, x...), y...)
	if _, err := ecdh.P256().NewPublicKey(raw); err != nil {
		return nil, errors.New("point is not on P-256")
	}
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}, nil
}

// NewJWK describes an ECDSA P-256 public key as a JWK.
func NewJWK(kid string, pub *ecdsa.PublicKey) JWK {
	return JWK{
		Kid: kid, Kty: "EC", Use: "sig", Alg: "ES256", Crv: "P-256",
		X: b64.EncodeToString(pad32(pub.X.Bytes())),
		Y: b64.EncodeToString(pad32(pub.Y.Bytes())),
	}
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid,omitempty"`
	Typ string `json:"typ,omitempty"`
}

// SignES256 creates a compact JWT signed with ES256. The mock ThaID uses it to
// issue ID tokens.
func SignES256(key *ecdsa.PrivateKey, kid string, claims any) (string, error) {
	h, err := json.Marshal(jwtHeader{Alg: "ES256", Kid: kid, Typ: "JWT"})
	if err != nil {
		return "", err
	}
	p, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	input := b64.EncodeToString(h) + "." + b64.EncodeToString(p)
	sum := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
	if err != nil {
		return "", err
	}
	sig := append(pad32(r.Bytes()), pad32(s.Bytes())...)
	return input + "." + b64.EncodeToString(sig), nil
}

// parseES256 splits a compact JWT, checks the algorithm and returns the parts
// needed for verification. The signature is not checked here.
func parseES256(token string) (hdr jwtHeader, payload []byte, signed string, sig []byte, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return hdr, nil, "", nil, errors.New("token is not a compact JWT")
	}
	hb, err := b64.DecodeString(parts[0])
	if err != nil {
		return hdr, nil, "", nil, errors.New("bad header encoding")
	}
	if err := json.Unmarshal(hb, &hdr); err != nil {
		return hdr, nil, "", nil, errors.New("bad header JSON")
	}
	// Accept only ES256. Never fall back to "none" or to an algorithm the
	// token chooses for itself.
	if hdr.Alg != "ES256" {
		return hdr, nil, "", nil, fmt.Errorf("unexpected alg %q", hdr.Alg)
	}
	payload, err = b64.DecodeString(parts[1])
	if err != nil {
		return hdr, nil, "", nil, errors.New("bad payload encoding")
	}
	sig, err = b64.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		return hdr, nil, "", nil, errors.New("bad signature encoding")
	}
	return hdr, payload, parts[0] + "." + parts[1], sig, nil
}

func verifyES256(pub *ecdsa.PublicKey, signed string, sig []byte) bool {
	sum := sha256.Sum256([]byte(signed))
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	return ecdsa.Verify(pub, sum[:], r, s)
}

func pad32(b []byte) []byte {
	if len(b) >= 32 {
		return b
	}
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}

// RandomString returns n random bytes encoded as base64url.
func RandomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b64.EncodeToString(b)
}

// PKCEChallenge returns the S256 code challenge for a verifier.
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return b64.EncodeToString(sum[:])
}
