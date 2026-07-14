package ctxauth

import (
	"creatorinsight/backend-go/internal/auth"

	"github.com/gin-gonic/gin"
)

const currentUserKey = "current_user"

func SetCurrentUser(ctx *gin.Context, user auth.CurrentUser) {
	ctx.Set(currentUserKey, user)
}

func CurrentUser(ctx *gin.Context) (auth.CurrentUser, bool) {
	value, ok := ctx.Get(currentUserKey)
	if !ok {
		return auth.CurrentUser{}, false
	}
	user, ok := value.(auth.CurrentUser)
	return user, ok
}
