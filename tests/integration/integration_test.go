// Copyright 2017 The Gitea Authors. All rights reserved.
// Copyright 2024 The Forgejo Authors c/o Codeberg e.V.. All rights reserved.
// SPDX-License-Identifier: MIT

//nolint:forbidigo
package integration

import (
	"bytes"
	"context"
	"fmt"
	"hash"
	"hash/fnv"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"forgejo.org/models/auth"
	"forgejo.org/models/db"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/graceful"
	"forgejo.org/modules/json"
	"forgejo.org/modules/jwtx"
	"forgejo.org/modules/keying"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/modules/testlogger"
	"forgejo.org/modules/util"
	"forgejo.org/modules/web"
	"forgejo.org/routers"
	auth_service "forgejo.org/services/auth"
	"forgejo.org/services/auth/source/remote"
	app_context "forgejo.org/services/context"
	"forgejo.org/services/mailer"
	user_service "forgejo.org/services/user"
	"forgejo.org/tests"

	"github.com/PuerkitoBio/goquery"
	"github.com/golang-jwt/jwt/v5"
	gouuid "github.com/google/uuid"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	goth_github "github.com/markbates/goth/providers/github"
	goth_gitlab "github.com/markbates/goth/providers/gitlab"
	"github.com/pquerna/otp/totp"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testWebRoutes *web.Route

type NilResponseRecorder struct {
	httptest.ResponseRecorder
	Length int
}

func (n *NilResponseRecorder) Write(b []byte) (int, error) {
	n.Length += len(b)
	return len(b), nil
}

// NewRecorder returns an initialized ResponseRecorder.
func NewNilResponseRecorder() *NilResponseRecorder {
	return &NilResponseRecorder{
		ResponseRecorder: *httptest.NewRecorder(),
	}
}

type NilResponseHashSumRecorder struct {
	httptest.ResponseRecorder
	Hash   hash.Hash
	Length int
}

func (n *NilResponseHashSumRecorder) Write(b []byte) (int, error) {
	_, _ = n.Hash.Write(b)
	n.Length += len(b)
	return len(b), nil
}

// NewRecorder returns an initialized ResponseRecorder.
func NewNilResponseHashSumRecorder() *NilResponseHashSumRecorder {
	return &NilResponseHashSumRecorder{
		Hash:             fnv.New32(),
		ResponseRecorder: *httptest.NewRecorder(),
	}
}

// runMainApp runs the subcommand and returns its standard output. Any returned error will usually be of type *ExitError. If c.Stderr was nil, Output populates ExitError.Stderr.
func runMainApp(subcommand string, args ...string) (string, error) {
	return tests.RunMainAppWithStdin(nil, subcommand, args...)
}

func TestMain(m *testing.M) {
	tests.DelegateToMainApp()

	defer log.GetManager().Close()

	managerCtx, cancel := context.WithCancel(context.Background())
	graceful.InitManager(managerCtx)
	defer cancel()

	tests.InitTest()
	testWebRoutes = routers.NormalRoutes()

	// integration test settings...
	if setting.CfgProvider != nil {
		testingCfg := setting.CfgProvider.Section("integration-tests")
		testlogger.SlowTest, _ = testingCfg.Key("SLOW_TEST").MustDuration(testlogger.SlowTest)
		testlogger.SlowFlush, _ = testingCfg.Key("SLOW_FLUSH").MustDuration(testlogger.SlowFlush)
	}

	if os.Getenv("GITEA_SLOW_TEST_TIME") != "" {
		duration, err := time.ParseDuration(os.Getenv("GITEA_SLOW_TEST_TIME"))
		if err == nil {
			testlogger.SlowTest = duration
		}
	}

	if os.Getenv("GITEA_SLOW_FLUSH_TIME") != "" {
		duration, err := time.ParseDuration(os.Getenv("GITEA_SLOW_FLUSH_TIME"))
		if err == nil {
			testlogger.SlowFlush = duration
		}
	}

	err := unittest.InitFixtures(
		unittest.FixturesOptions{
			Dir: filepath.Join(setting.AppWorkPath, "models/fixtures/"),
		},
	)
	if err != nil {
		fmt.Printf("Error initializing test database: %v\n", err)
		os.Exit(1)
	}

	// FIXME: the console logger is deleted by mistake, so if there is any `log.Fatal`, developers won't see any error message.
	// Instead, "No tests were found",  last nonsense log is "According to the configuration, subsequent logs will not be printed to the console"
	exitCode := m.Run()

	if err := testlogger.WriterCloser.Reset(); err != nil {
		fmt.Printf("testlogger.WriterCloser.Reset: error ignored: %v\n", err)
	}

	if err = util.RemoveAll(setting.Indexer.IssuePath); err != nil {
		fmt.Printf("util.RemoveAll: %v\n", err)
		os.Exit(1)
	}
	if err = util.RemoveAll(setting.Indexer.RepoPath); err != nil {
		fmt.Printf("Unable to remove repo indexer: %v\n", err)
		os.Exit(1)
	}

	os.Exit(exitCode)
}

type TestSession struct {
	jar http.CookieJar
}

func (s *TestSession) GetCookie(name string) *http.Cookie {
	baseURL, err := url.Parse(setting.AppURL)
	if err != nil {
		return nil
	}

	for _, c := range s.jar.Cookies(baseURL) {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func (s *TestSession) SetCookie(cookie *http.Cookie) *http.Cookie {
	baseURL, err := url.Parse(setting.AppURL)
	if err != nil {
		return nil
	}

	s.jar.SetCookies(baseURL, []*http.Cookie{cookie})
	return nil
}

func (s *TestSession) MakeRequest(t testing.TB, rw *RequestWrapper, expectedStatus int) *httptest.ResponseRecorder {
	t.Helper()
	req := rw.Request
	baseURL, err := url.Parse(setting.AppURL)
	require.NoError(t, err)
	for _, c := range s.jar.Cookies(baseURL) {
		req.AddCookie(c)
	}
	resp := MakeRequest(t, rw, expectedStatus)

	ch := http.Header{}
	ch.Add("Cookie", strings.Join(resp.Header()["Set-Cookie"], ";"))
	cr := http.Request{Header: ch}
	s.jar.SetCookies(baseURL, cr.Cookies())

	return resp
}

func (s *TestSession) MakeRequestNilResponseRecorder(t testing.TB, rw *RequestWrapper, expectedStatus int) *NilResponseRecorder {
	t.Helper()
	req := rw.Request
	baseURL, err := url.Parse(setting.AppURL)
	require.NoError(t, err)
	for _, c := range s.jar.Cookies(baseURL) {
		req.AddCookie(c)
	}
	resp := MakeRequestNilResponseRecorder(t, rw, expectedStatus)

	ch := http.Header{}
	ch.Add("Cookie", strings.Join(resp.Header()["Set-Cookie"], ";"))
	cr := http.Request{Header: ch}
	s.jar.SetCookies(baseURL, cr.Cookies())

	return resp
}

func (s *TestSession) MakeRequestNilResponseHashSumRecorder(t testing.TB, rw *RequestWrapper, expectedStatus int) *NilResponseHashSumRecorder {
	t.Helper()
	req := rw.Request
	baseURL, err := url.Parse(setting.AppURL)
	require.NoError(t, err)
	for _, c := range s.jar.Cookies(baseURL) {
		req.AddCookie(c)
	}
	resp := MakeRequestNilResponseHashSumRecorder(t, rw, expectedStatus)

	ch := http.Header{}
	ch.Add("Cookie", strings.Join(resp.Header()["Set-Cookie"], ";"))
	cr := http.Request{Header: ch}
	s.jar.SetCookies(baseURL, cr.Cookies())

	return resp
}

func (s *TestSession) EnrollTOTP(t testing.TB) {
	t.Helper()

	req := NewRequest(t, "GET", "/user/settings/security/two_factor/enroll")
	resp := s.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	totpSecretKey, has := htmlDoc.Find(".twofa img[src^='data:image/png;base64']").Attr("alt")
	assert.True(t, has)

	currentTOTP, err := totp.GenerateCode(totpSecretKey, time.Now())
	require.NoError(t, err)

	req = NewRequestWithValues(t, "POST", "/user/settings/security/two_factor/enroll", map[string]string{
		"passcode": currentTOTP,
	})
	s.MakeRequest(t, req, http.StatusSeeOther)

	flashCookie := s.GetCookie(app_context.CookieNameFlash)
	assert.NotNil(t, flashCookie)
	assert.Contains(t, flashCookie.Value, "success%3DYour%2Baccount%2Bhas%2Bbeen%2Bsuccessfully%2Benrolled.")
}

const userPassword = "password"

func emptyTestSession(t testing.TB) *TestSession {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)

	return &TestSession{jar: jar}
}

func getUserToken(t testing.TB, userName string, scope ...auth.AccessTokenScope) string {
	return getTokenForLoggedInUser(t, loginUser(t, userName), scope...)
}

func mockCompleteUserAuth(mock func(res http.ResponseWriter, req *http.Request) (goth.User, error)) func() {
	old := gothic.CompleteUserAuth
	gothic.CompleteUserAuth = mock
	return func() {
		gothic.CompleteUserAuth = old
	}
}

func addAuthSource(t *testing.T, payload map[string]string) *auth.Source {
	session := loginUser(t, "user1")
	req := NewRequestWithValues(t, "POST", "/admin/auths/new", payload)
	session.MakeRequest(t, req, http.StatusSeeOther)
	return unittest.AssertExistsAndLoadBean(t, &auth.Source{Name: payload["name"]})
}

func authSourcePayloadOAuth2(name string) map[string]string {
	return map[string]string{
		"type":      fmt.Sprintf("%d", auth.OAuth2),
		"name":      name,
		"is_active": "on",
	}
}

func authSourcePayloadOpenIDConnect(name, appURL string) map[string]string {
	payload := authSourcePayloadOAuth2(name)
	payload["oauth2_provider"] = "openidConnect"
	payload["open_id_connect_auto_discovery_url"] = appURL + ".well-known/openid-configuration"
	return payload
}

func authSourcePayloadGitLab(name string) map[string]string {
	payload := authSourcePayloadOAuth2(name)
	payload["oauth2_provider"] = "gitlab"
	return payload
}

func authSourcePayloadGitLabCustom(name string) map[string]string {
	payload := authSourcePayloadGitLab(name)
	payload["oauth2_use_custom_url"] = "on"
	payload["oauth2_auth_url"] = goth_gitlab.AuthURL
	payload["oauth2_token_url"] = goth_gitlab.TokenURL
	payload["oauth2_profile_url"] = goth_gitlab.ProfileURL
	return payload
}

func authSourcePayloadGitHub(name string) map[string]string {
	payload := authSourcePayloadOAuth2(name)
	payload["oauth2_provider"] = "github"
	return payload
}

func authSourcePayloadGitHubCustom(name string) map[string]string {
	payload := authSourcePayloadGitHub(name)
	payload["oauth2_use_custom_url"] = "on"
	payload["oauth2_auth_url"] = goth_github.AuthURL
	payload["oauth2_token_url"] = goth_github.TokenURL
	payload["oauth2_profile_url"] = goth_github.ProfileURL
	return payload
}

func createRemoteAuthSource(t *testing.T, name, url, matchingSource string) *auth.Source {
	require.NoError(t, auth.CreateSource(t.Context(), &auth.Source{
		Type:     auth.Remote,
		Name:     name,
		IsActive: true,
		Cfg: &remote.Source{
			URL:            url,
			MatchingSource: matchingSource,
		},
	}))
	return unittest.AssertExistsAndLoadBean(t, &auth.Source{Name: name})
}

func createUser(ctx context.Context, t testing.TB, user *user_model.User) func() {
	user.MustChangePassword = false
	user.LowerName = strings.ToLower(user.Name)

	require.NoError(t, db.Insert(ctx, user))

	if len(user.Email) > 0 {
		require.NoError(t, user_service.ReplacePrimaryEmailAddress(ctx, user, user.Email))
	}

	return func() {
		require.NoError(t, user_service.DeleteUser(ctx, user, true))
	}
}

func loginUser(t testing.TB, userName string) *TestSession {
	t.Helper()

	return loginUserWithPassword(t, userName, userPassword)
}

func loginUserWithPassword(t testing.TB, userName, password string) *TestSession {
	t.Helper()

	return loginUserWithPasswordRemember(t, userName, password, false)
}

func loginUserWithPasswordRemember(t testing.TB, userName, password string, rememberMe bool) *TestSession {
	t.Helper()
	req := NewRequestWithValues(t, "POST", "/user/login", map[string]string{
		"user_name": userName,
		"password":  password,
		"remember":  strconv.FormatBool(rememberMe),
	})
	resp := MakeRequest(t, req, http.StatusSeeOther)

	ch := http.Header{}
	ch.Add("Cookie", strings.Join(resp.Header()["Set-Cookie"], ";"))
	cr := http.Request{Header: ch}

	session := emptyTestSession(t)

	baseURL, err := url.Parse(setting.AppURL)
	require.NoError(t, err)
	session.jar.SetCookies(baseURL, cr.Cookies())

	return session
}

func loginUserWithTOTP(t testing.TB, user *user_model.User) *TestSession {
	t.Helper()
	session := loginUser(t, user.Name)

	twoFactor, err := auth.GetTwoFactorByUID(db.DefaultContext, user.ID)
	require.NoError(t, err)

	key := keying.TOTP
	code, err := key.Decrypt(twoFactor.Secret, keying.ColumnAndID("secret", twoFactor.ID))
	require.NoError(t, err)

	passcode, err := totp.GenerateCode(string(code), time.Now())
	require.NoError(t, err)

	req := NewRequestWithValues(t, "POST", "/user/two_factor", map[string]string{
		"passcode": passcode,
	})
	session.MakeRequest(t, req, http.StatusSeeOther)

	return session
}

func loginUserMaybeTOTP(t testing.TB, user *user_model.User, useTOTP bool) *TestSession {
	if useTOTP {
		sess := loginUser(t, user.Name)
		sess.EnrollTOTP(t)
		sess.MakeRequest(t, NewRequest(t, "POST", "/user/logout"), http.StatusSeeOther)

		return loginUserWithTOTP(t, user)
	}
	return loginUser(t, user.Name)
}

// token has to be unique this counter take care of
var tokenCounter atomic.Int64

// getTokenForLoggedInUser returns a token for a logged in user.
// The scope is an optional list of snake_case strings like the frontend form fields,
// but without the "scope_" prefix.
func getTokenForLoggedInUser(t testing.TB, session *TestSession, scopes ...auth.AccessTokenScope) string {
	t.Helper()
	accessTokenName := fmt.Sprintf("api-testing-token-%d", tokenCounter.Add(1))
	createApplicationSettingsToken(t, session, accessTokenName, scopes...)
	token := assertAccessToken(t, session)
	return token
}

// createApplicationSettingsToken creates a token with given name and scopes for the currently logged in user.
// It will redirect to the application settings page.
func createApplicationSettingsToken(t testing.TB, session *TestSession, name string, scopes ...auth.AccessTokenScope) {
	require.NotEmpty(t, scopes, "attempted to create access token with no scopes, which is not valid")

	urlValues := url.Values{}
	urlValues.Add("name", name)
	publicOnly := false
	for _, scope := range scopes {
		if scope == auth.AccessTokenScopePublicOnly {
			publicOnly = true
		} else {
			urlValues.Add("scope", string(scope))
		}
	}
	if publicOnly {
		urlValues.Add("resource", "public-only")
	} else {
		urlValues.Add("resource", "all")
	}
	req := NewRequestWithURLValues(t, "POST", "/user/settings/applications/tokens/new", urlValues)
	resp := session.MakeRequest(t, req, http.StatusSeeOther)

	// Log the flash values on failure
	if !assert.Equal(t, []string{"/user/settings/applications"}, resp.Result().Header["Location"]) {
		for _, cookie := range resp.Result().Cookies() {
			if cookie.Name != app_context.CookieNameFlash {
				continue
			}
			flash, _ := url.ParseQuery(cookie.Value)
			for key, value := range flash {
				t.Logf("Flash %q: %q", key, value)
			}
		}
	}
}

// TODO: currently this is implemented with direct DB access, which is somewhat against the grain for the integration
// tests.  But fine-grained repo access tokens don't currently have an API or Web UI to create or manage them.  This
// should be reimplemented when one of those alternatives lands.
func createFineGrainedRepoAccessToken(t testing.TB, username string, scopes []auth.AccessTokenScope, repoIDs []int64) string {
	user, err := user_model.GetUserByName(t.Context(), username)
	require.NoError(t, err)

	scopesStr := make([]string, len(scopes))
	for i := range scopes {
		scopesStr[i] = string(scopes[i])
	}
	scope, err := auth.AccessTokenScope(strings.Join(scopesStr, ",")).Normalize()
	require.NoError(t, err)

	token := &auth.AccessToken{
		UID:              user.ID,
		Name:             "integration test token",
		Scope:            scope,
		ResourceAllRepos: false,
	}
	err = auth.NewAccessToken(t.Context(), token)
	require.NoError(t, err)

	for _, id := range repoIDs {
		resource := &auth.AccessTokenResourceRepo{
			TokenID: token.ID,
			RepoID:  id,
		}
		_, err = db.GetEngine(t.Context()).Insert(resource)
		require.NoError(t, err)
	}

	return token.Token
}

// assertAccessToken retrieves a token from "/user/settings/applications" and returns it.
// It will also assert that the page contains a token.
func assertAccessToken(t testing.TB, session *TestSession) string {
	req := NewRequest(t, "GET", "/user/settings/applications")
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)
	token := htmlDoc.doc.Find(".ui.info p").Text()
	assert.NotEmpty(t, token)
	return token
}

type RequestWrapper struct {
	*http.Request
}

func (req *RequestWrapper) AddBasicAuth(username string) *RequestWrapper {
	req.Request.SetBasicAuth(username, userPassword)
	return req
}

func (req *RequestWrapper) AddTokenAuth(token string) *RequestWrapper {
	if token == "" {
		return req
	}
	if !strings.HasPrefix(token, "Bearer ") {
		token = "Bearer " + token
	}
	req.Request.Header.Set("Authorization", token)
	return req
}

func (req *RequestWrapper) SetHeader(name, value string) *RequestWrapper {
	req.Request.Header.Set(name, value)
	return req
}

func NewRequest(t testing.TB, method, urlStr string) *RequestWrapper {
	t.Helper()
	return NewRequestWithBody(t, method, urlStr, nil)
}

func NewRequestf(t testing.TB, method, urlFormat string, args ...any) *RequestWrapper {
	t.Helper()
	return NewRequest(t, method, fmt.Sprintf(urlFormat, args...))
}

func NewRequestWithValues(t testing.TB, method, urlStr string, values map[string]string) *RequestWrapper {
	t.Helper()
	urlValues := url.Values{}
	for key, value := range values {
		urlValues[key] = []string{value}
	}
	return NewRequestWithURLValues(t, method, urlStr, urlValues)
}

func NewRequestWithURLValues(t testing.TB, method, urlStr string, urlValues url.Values) *RequestWrapper {
	t.Helper()
	return NewRequestWithBody(t, method, urlStr, bytes.NewBufferString(urlValues.Encode())).
		SetHeader("Content-Type", "application/x-www-form-urlencoded")
}

func NewRequestWithJSON(t testing.TB, method, urlStr string, v any) *RequestWrapper {
	t.Helper()

	jsonBytes, err := json.Marshal(v)
	require.NoError(t, err)
	return NewRequestWithBody(t, method, urlStr, bytes.NewBuffer(jsonBytes)).
		SetHeader("Content-Type", "application/json")
}

func NewRequestWithBody(t testing.TB, method, urlStr string, body io.Reader) *RequestWrapper {
	t.Helper()
	if !strings.HasPrefix(urlStr, "http") && !strings.HasPrefix(urlStr, "/") {
		urlStr = "/" + urlStr
	}
	req, err := http.NewRequest(method, urlStr, body)
	require.NoError(t, err)
	req.RequestURI = urlStr

	return &RequestWrapper{req}
}

const NoExpectedStatus = -1

func MakeRequest(t testing.TB, rw *RequestWrapper, expectedStatus int) *httptest.ResponseRecorder {
	t.Helper()
	req := rw.Request
	recorder := httptest.NewRecorder()
	if req.RemoteAddr == "" {
		req.RemoteAddr = "test-mock:12345"
	}
	testWebRoutes.ServeHTTP(recorder, req)
	if expectedStatus != NoExpectedStatus {
		if !assert.Equal(t, expectedStatus, recorder.Code, "Request: %s %s", req.Method, req.URL.String()) {
			logUnexpectedResponse(t, recorder)
		}
	}
	return recorder
}

func MakeRequestNilResponseRecorder(t testing.TB, rw *RequestWrapper, expectedStatus int) *NilResponseRecorder {
	t.Helper()
	req := rw.Request
	recorder := NewNilResponseRecorder()
	testWebRoutes.ServeHTTP(recorder, req)
	if expectedStatus != NoExpectedStatus {
		if !assert.Equal(t, expectedStatus, recorder.Code,
			"Request: %s %s", req.Method, req.URL.String()) {
			logUnexpectedResponse(t, &recorder.ResponseRecorder)
		}
	}
	return recorder
}

func MakeRequestNilResponseHashSumRecorder(t testing.TB, rw *RequestWrapper, expectedStatus int) *NilResponseHashSumRecorder {
	t.Helper()
	req := rw.Request
	recorder := NewNilResponseHashSumRecorder()
	testWebRoutes.ServeHTTP(recorder, req)
	if expectedStatus != NoExpectedStatus {
		if !assert.Equal(t, expectedStatus, recorder.Code,
			"Request: %s %s", req.Method, req.URL.String()) {
			logUnexpectedResponse(t, &recorder.ResponseRecorder)
		}
	}
	return recorder
}

// logUnexpectedResponse logs the contents of an unexpected response.
func logUnexpectedResponse(t testing.TB, recorder *httptest.ResponseRecorder) {
	t.Helper()
	respBytes := recorder.Body.Bytes()
	if len(respBytes) == 0 {
		// log the content of the flash cookie
		for _, cookie := range recorder.Result().Cookies() {
			if cookie.Name != app_context.CookieNameFlash {
				continue
			}
			flash, _ := url.ParseQuery(cookie.Value)
			for key, value := range flash {
				// the key is itself url-encoded
				if flash, err := url.ParseQuery(key); err == nil {
					for key, value := range flash {
						t.Logf("FlashCookie %q: %q", key, value)
					}
				} else {
					t.Logf("FlashCookie %q: %q", key, value)
				}
			}
		}

		return
	} else if len(respBytes) < 2048 {
		// if body is short, just log the whole thing
		t.Log("Response: ", string(respBytes))
		return
	}
	t.Log("Response length: ", len(respBytes))

	// log the "flash" error message, if one exists
	// we must create a new buffer, so that we don't "use up" resp.Body
	htmlDoc, err := goquery.NewDocumentFromReader(bytes.NewBuffer(respBytes))
	if err != nil {
		return // probably a non-HTML response
	}
	errMsg := htmlDoc.Find(".ui.negative.message").Text()
	if len(errMsg) > 0 {
		t.Log("A flash error message was found:", errMsg)
	}
}

func DecodeJSON(t testing.TB, resp *httptest.ResponseRecorder, v any) {
	t.Helper()

	decoder := json.NewDecoder(resp.Body)
	require.NoError(t, decoder.Decode(v))
}

func VerifyJSONSchema(t testing.TB, resp *httptest.ResponseRecorder, schemaFile string) {
	t.Helper()

	schemaFilePath := filepath.Join(setting.AppWorkPath, "tests", "integration", "schemas", schemaFile)
	_, schemaFileErr := os.Stat(schemaFilePath)
	require.NoError(t, schemaFileErr)

	schema, err := jsonschema.NewCompiler().Compile(schemaFilePath)
	require.NoError(t, err)

	var data any
	err = json.Unmarshal(resp.Body.Bytes(), &data)
	require.NoError(t, err)

	schemaValidation := schema.Validate(data)
	require.NoError(t, schemaValidation)
}

func GetHTMLTitle(t testing.TB, session *TestSession, urlStr string) string {
	t.Helper()

	doc := getHTMLDoc(t, session, urlStr, http.StatusOK)
	return doc.Find("head title").Text()
}

// getHTMLDoc gets HTMLDoc from url with expected status. Use status
// NoExpectedStatus to ignore status.
func getHTMLDoc(t testing.TB, session *TestSession, urlStr string, expectedStatus int) *HTMLDoc {
	t.Helper()

	req := NewRequest(t, "GET", urlStr)
	var resp *httptest.ResponseRecorder
	if session == nil {
		resp = MakeRequest(t, req, expectedStatus)
	} else {
		resp = session.MakeRequest(t, req, expectedStatus)
	}
	return NewHTMLParser(t, resp.Body)
}

func sessionJSONMethod(t testing.TB, session *TestSession, method, endpoint string, opts any, expectedStatus int,
) *httptest.ResponseRecorder {
	req := NewRequestWithJSON(t, method, endpoint, &opts)
	return session.MakeRequest(t, req, expectedStatus)
}

func sessionJSONPOST(t testing.TB, session *TestSession, endpoint string, opts any, expectedStatus int,
) *httptest.ResponseRecorder {
	return sessionJSONMethod(t, session, "POST", endpoint, opts, expectedStatus)
}

func sessionJSONPUT(t testing.TB, session *TestSession, endpoint string, opts any, expectedStatus int,
) *httptest.ResponseRecorder {
	return sessionJSONMethod(t, session, "PUT", endpoint, opts, expectedStatus)
}

func sessionMethod(t testing.TB, session *TestSession, method, endpoint string, expectedStatus int,
) *httptest.ResponseRecorder {
	req := NewRequest(t, method, endpoint)
	return session.MakeRequest(t, req, expectedStatus)
}

func sessionPOST(t testing.TB, session *TestSession, endpoint string, expectedStatus int,
) *httptest.ResponseRecorder {
	return sessionMethod(t, session, "POST", endpoint, expectedStatus)
}

func sessionGET(t testing.TB, session *TestSession, endpoint string, expectedStatus int,
) *httptest.ResponseRecorder {
	return sessionMethod(t, session, "GET", endpoint, expectedStatus)
}

func sessionDELETE(t testing.TB, session *TestSession, endpoint string, expectedStatus int,
) *httptest.ResponseRecorder {
	return sessionMethod(t, session, "DELETE", endpoint, expectedStatus)
}

func SortMailerMessages(msgs []*mailer.Message) {
	slices.SortFunc(msgs, func(a, b *mailer.Message) int {
		return strings.Compare(b.To, a.To)
	})
}

type AuthorizedIntegrationTester struct {
	t                       *testing.T
	authorizedIntegration   *auth.AuthorizedIntegration
	jwtSigningKey           jwtx.SigningKey
	testServer              *httptest.Server
	resetHTTPClient         func()
	resetAllowLocalNetworks func()
}

func newAITester(t *testing.T, setupAI ...func(*auth.AuthorizedIntegration)) *AuthorizedIntegrationTester {
	ait := &AuthorizedIntegrationTester{
		t: t,
	}

	var jwtSigningKey jwtx.SigningKey
	keyPath := filepath.Join(t.TempDir(), "jwt-rsa-2048.priv")
	jwtSigningKey, err := jwtx.InitAsymmetricSigningKey(keyPath, "RS256")
	require.NoError(t, err)
	ait.jwtSigningKey = jwtSigningKey

	uniqueTest := gouuid.New().String()
	ait.testServer = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == fmt.Sprintf("/api/actions/%s/.well-known/openid-configuration", uniqueTest) {
			retval := map[string]any{
				"issuer":                                ait.authorizedIntegration.Issuer,
				"jwks_uri":                              fmt.Sprintf("%s/.keys", ait.authorizedIntegration.Issuer),
				"id_token_signing_alg_values_supported": []string{"RS256"},
			}
			err := json.NewEncoder(w).Encode(retval)
			require.NoError(t, err)
			return
		}
		if r.URL.Path == fmt.Sprintf("/api/actions/%s/.keys", uniqueTest) {
			jwk, err := ait.jwtSigningKey.ToJWK()
			require.NoError(t, err)
			jwk["use"] = "sig"
			retval := map[string]any{
				"keys": []map[string]string{jwk},
			}
			_ = json.NewEncoder(w).Encode(retval) // no error checking -- some tests abort read
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	// trust TLS cert of our NewTLSServer  by inserting the test client for our test server in as the HTTP client to use
	ait.resetHTTPClient = test.MockVariableValue(
		&auth_service.GetAuthorizedIntegrationHTTPClient,
		func() *http.Client {
			return ait.testServer.Client()
		},
	)

	ait.authorizedIntegration = &auth.AuthorizedIntegration{
		UserID:   2,
		Scope:    auth.AccessTokenScopeAll,
		Issuer:   fmt.Sprintf("%s/api/actions/%s", ait.testServer.URL, uniqueTest),
		Audience: fmt.Sprintf("https://forgejo.example.org/-/coolguy/authorized-integration/%s", gouuid.New().String()),
		ClaimRules: &auth.ClaimRules{
			Rules: []auth.ClaimRule{
				{
					Claim:      "custom-claim",
					Comparison: auth.ClaimEqual,
					Value:      "custom-claim-value",
				},
			},
		},
		ResourceAllRepos: true,
		Name:             fmt.Sprintf("AI %s", t.Name()),
		Description:      fmt.Sprintf("An Authorized Integration created for the test case %s.\nIt's pretty neat.", t.Name()),
		UI:               auth.AuthorizedIntegrationUIGeneric,
	}
	for _, setup := range setupAI {
		setup(ait.authorizedIntegration)
	}
	_, err = db.GetEngine(t.Context()).Insert(ait.authorizedIntegration)
	require.NoError(t, err)

	ait.resetAllowLocalNetworks = test.MockVariableValue(&setting.AuthorizedIntegration.AllowLocalNetworks, true)

	return ait
}

func (ait *AuthorizedIntegrationTester) signedJWT() string {
	claims := jwt.MapClaims{
		"iss":          ait.authorizedIntegration.Issuer,
		"aud":          ait.authorizedIntegration.Audience,
		"custom-claim": "custom-claim-value",
	}
	signedToken, err := ait.jwtSigningKey.JWT(claims)
	require.NoError(ait.t, err)
	return signedToken
}

func (ait *AuthorizedIntegrationTester) close() {
	ait.resetAllowLocalNetworks()
	ait.resetHTTPClient()
	ait.testServer.Close()
}
