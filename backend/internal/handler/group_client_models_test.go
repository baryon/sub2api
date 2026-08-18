package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestListUserGroupModelsRejectsInvalidGroupID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/groups/abc/models", nil)
	c.Params = gin.Params{{Key: "id", Value: "abc"}}
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 1})

	h := &GatewayHandler{apiKeyService: &service.APIKeyService{}}
	h.ListUserGroupModels(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var got response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "Invalid group ID", got.Message)
}

func TestListUserGroupModelsRequiresAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/groups/1/models", nil)
	c.Params = gin.Params{{Key: "id", Value: "1"}}

	h := &GatewayHandler{apiKeyService: &service.APIKeyService{}}
	h.ListUserGroupModels(c)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
