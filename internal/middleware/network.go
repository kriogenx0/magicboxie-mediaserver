package middleware

import (
	"github.com/gin-gonic/gin"
)

// NetworkHeader is set by the Pi's nginx (deploy/pi/nginx/magicboxie.conf)
// on every request: "lan" for a client on the home network that talked to the
// Pi directly, "public" for anything else, including everything the VPS
// forwards from magicboxie.homecloud.one.
const NetworkHeader = "X-MagicBoxie-Network"

const networkKey = "magicboxie.network"

type network int

const (
	networkUnknown network = iota // proxy not trusted: behave as before
	networkLAN
	networkPublic
)

// DetectNetwork records which network a request came from. With trust off
// (any deployment whose proxy doesn't set NetworkHeader) every request is
// "unknown" and auth works exactly as it always has.
func DetectNetwork(trust bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		n := networkUnknown
		if trust {
			n = networkPublic
			if c.GetHeader(NetworkHeader) == "lan" {
				n = networkLAN
			}
		}
		c.Set(networkKey, n)
		c.Next()
	}
}

// IsLAN reports whether the request came from the home network, which lets
// it in without a password.
func IsLAN(c *gin.Context) bool {
	n, _ := c.Get(networkKey)
	return n == networkLAN
}

// IsPublic reports whether the request came through the public address, so
// even endpoints that are open at home need a password.
func IsPublic(c *gin.Context) bool {
	n, _ := c.Get(networkKey)
	return n == networkPublic
}
