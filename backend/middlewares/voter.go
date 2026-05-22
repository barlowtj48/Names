package middlewares

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/barlowtj48/names/shared/secrets"
	"github.com/gin-gonic/gin"
)

// VoterIdentity derives a stable, anonymous per-voter hash from the
// FingerprintJS visitorId header combined with the client IP and a server salt.
// Stored as a hex SHA-256; the raw fingerprint and IP are not persisted.
//
// Header: X-Voter-Fingerprint (provided by static/app.js using FingerprintJS OSS).
// If the header is missing, we still compute a hash from IP+UA so requests can be
// rate-limited, but submit/vote handlers will reject those.
const VoterHashKey = "voter_hash"
const HasFingerprintKey = "has_fingerprint"

func VoterIdentity() gin.HandlerFunc {
	salt := secrets.Get().VoterSalt
	return func(c *gin.Context) {
		fp := c.GetHeader("X-Voter-Fingerprint")
		ip := c.ClientIP()
		ua := c.GetHeader("User-Agent")

		h := sha256.New()
		h.Write([]byte(salt))
		h.Write([]byte{0})
		h.Write([]byte(fp))
		h.Write([]byte{0})
		h.Write([]byte(ip))
		h.Write([]byte{0})
		h.Write([]byte(ua))
		c.Set(VoterHashKey, hex.EncodeToString(h.Sum(nil)))
		c.Set(HasFingerprintKey, fp != "")
		c.Next()
	}
}

func RequireFingerprint() gin.HandlerFunc {
	return func(c *gin.Context) {
		if has, _ := c.Get(HasFingerprintKey); has != true {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "voter fingerprint required",
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
