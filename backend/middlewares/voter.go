package middlewares

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"

	"github.com/barlowtj48/names/shared/secrets"
	"github.com/gin-gonic/gin"
)

// VoterIdentity derives a stable, anonymous per-voter hash from a long-lived
// HttpOnly cookie. IP and User-Agent are intentionally NOT part of the hash so
// that switching mobile networks or browser updates do not orphan votes. The
// optional X-Voter-Fingerprint header (FingerprintJS visitorId) is used only as
// the cookie seed on first contact, giving best-effort recovery on a device
// whose cookies were cleared but whose fingerprint is still stable.
const (
	VoterHashKey      = "voter_hash"
	HasFingerprintKey = "has_fingerprint"
	voterCookieName   = "voter_id"
	cookieMaxAgeSecs  = 60 * 60 * 24 * 365 * 10 // ~10 years
	hashPrefix        = "v2:"
)

func VoterIdentity() gin.HandlerFunc {
	cfg := secrets.Get()
	salt := cfg.VoterSalt
	secure := cfg.IsProduction()
	return func(c *gin.Context) {
		cookieID, _ := c.Cookie(voterCookieName)
		if cookieID == "" {
			if fp := c.GetHeader("X-Voter-Fingerprint"); fp != "" {
				cookieID = fp
			} else {
				cookieID = newCookieID()
			}
			c.SetSameSite(http.SameSiteLaxMode)
			c.SetCookie(voterCookieName, cookieID, cookieMaxAgeSecs, "/", "", secure, true)
		}
		c.Set(VoterHashKey, voterHashFor(salt, cookieID))
		c.Set(HasFingerprintKey, true)
		c.Next()
	}
}

func RequireFingerprint() gin.HandlerFunc {
	return func(c *gin.Context) {
		if has, _ := c.Get(HasFingerprintKey); has != true {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "voter identity required",
			})
			return
		}
		c.Next()
	}
}

func VoterHash(c *gin.Context) string {
	v, _ := c.Get(VoterHashKey)
	s, _ := v.(string)
	return s
}

func newCookieID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func voterHashFor(salt, id string) string {
	mac := hmac.New(sha256.New, []byte(salt))
	mac.Write([]byte(hashPrefix))
	mac.Write([]byte(id))
	return hex.EncodeToString(mac.Sum(nil))
}
