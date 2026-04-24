// Package middleware provides Gin middleware for the OJ API server.
package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/your-org/my-oj/internal/models"
)

// contextKey type prevents key collisions in gin.Context values.
type contextKey string

const (
	ContextKeyUserID   contextKey = "user_id"
	ContextKeyUserRole contextKey = "user_role"
)

// Claims is the JWT payload issued by the auth service.
type Claims struct {
	UserID models.ID  `json:"uid"`
	Role   models.UserRole `json:"role"`
	jwt.RegisteredClaims
}

// Auth returns a Gin middleware that validates Bearer JWT tokens.
// Requests without a valid token receive 401.
// The parsed UserID and Role are stored in gin.Context for downstream handlers.
func Auth(signingKey []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.GetHeader("Authorization")
		if !strings.HasPrefix(raw, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		tokenStr := raw[len("Bearer "):]

		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return signingKey, nil
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}

		c.Set(string(ContextKeyUserID), claims.UserID)
		c.Set(string(ContextKeyUserRole), claims.Role)
		c.Next()
	}
}

// RequireRole returns a middleware that rejects requests whose role is not in
// the allowed set.  Must be placed after Auth.
func RequireRole(roles ...models.UserRole) gin.HandlerFunc {
	allowed := make(map[models.UserRole]struct{}, len(roles))
	for _, r := range roles {
		allowed[r] = struct{}{}
	}
	return func(c *gin.Context) {
		roleVal, _ := c.Get(string(ContextKeyUserRole))
		role, _ := roleVal.(models.UserRole)
		if _, ok := allowed[role]; !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
			return
		}
		c.Next()
	}
}

// UserIDFromCtx extracts the authenticated user ID from a Gin context.
func UserIDFromCtx(c *gin.Context) (models.ID, bool) {
	v, ok := c.Get(string(ContextKeyUserID))
	if !ok {
		return 0, false
	}
	id, ok := v.(models.ID)
	return id, ok
}

// OptionalAuth tries to parse a Bearer token but never rejects the request.
// Downstream handlers can check UserIDFromCtx to see if the user is authenticated.
func OptionalAuth(signingKey []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.GetHeader("Authorization")
		if !strings.HasPrefix(raw, "Bearer ") {
			c.Next()
			return
		}
		tokenStr := raw[len("Bearer "):]
		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return signingKey, nil
		})
		if err == nil && token.Valid {
			c.Set(string(ContextKeyUserID), claims.UserID)
			c.Set(string(ContextKeyUserRole), claims.Role)
		}
		c.Next()
	}
}
