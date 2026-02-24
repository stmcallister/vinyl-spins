package discogs

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	BaseURL = "https://api.discogs.com"
)

// Client is a small Discogs API client. This is a minimal local copy of the
// pieces of github.com/stmcallister/go-discogs used by this repo.
type Client struct {
	apiKey string

	consumerKey    string
	consumerSecret string
	oauthToken     string
	oauthSecret    string

	baseURL    string
	userAgent  string
	HTTPClient *http.Client
}

// NewOAuthClient creates a client that signs requests with OAuth 1.0a.
// Pass empty accessToken/accessSecret for the initial request token step.
func NewOAuthClient(consumerKey, consumerSecret, accessToken, accessSecret string) *Client {
	return &Client{
		consumerKey:    consumerKey,
		consumerSecret: consumerSecret,
		oauthToken:     accessToken,
		oauthSecret:    accessSecret,
		HTTPClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
		baseURL: BaseURL,
	}
}

// WithUserAgent sets a Discogs-compliant user agent (required by Discogs policy).
func (c *Client) WithUserAgent(ua string) *Client {
	c.userAgent = ua
	return c
}

type errorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) sendRequest(req *http.Request, v any) error {
	req.Header.Set("Accept", "application/json; charset=utf-8")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	// Auth: prefer OAuth if configured; else fall back to personal token.
	if c.consumerKey != "" && c.consumerSecret != "" {
		req.Header.Set("Authorization", oauth1AuthorizationHeader(req.Method, req.URL.String(), c.consumerKey, c.consumerSecret, c.oauthToken, c.oauthSecret, nil))
	} else if c.apiKey != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Discogs token=%s", c.apiKey))
	}

	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		var errRes errorResponse
		if err = json.NewDecoder(res.Body).Decode(&errRes); err == nil && errRes.Message != "" {
			return errors.New(errRes.Message)
		}
		return fmt.Errorf("discogs request failed: status %d", res.StatusCode)
	}

	return json.NewDecoder(res.Body).Decode(v)
}

// ---- OAuth 1.0a endpoints ----

// RequestToken starts the OAuth 1.0a flow.
func (c *Client) RequestToken(ctx context.Context, callbackURL string) (token string, secret string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/oauth/request_token", nil)
	if err != nil {
		return "", "", err
	}
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	req.Header.Set("Authorization", oauth1AuthorizationHeader(http.MethodPost, req.URL.String(), c.consumerKey, c.consumerSecret, "", "", map[string]string{
		"oauth_callback": callbackURL,
	}))

	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", "", fmt.Errorf("status %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	vals, err := url.ParseQuery(string(body))
	if err != nil {
		return "", "", err
	}
	token = vals.Get("oauth_token")
	secret = vals.Get("oauth_token_secret")
	if token == "" || secret == "" {
		return "", "", fmt.Errorf("unexpected response: %s", strings.TrimSpace(string(body)))
	}
	return token, secret, nil
}

// AccessToken completes the OAuth 1.0a flow.
func (c *Client) AccessToken(ctx context.Context, requestToken, requestSecret, verifier string) (token string, secret string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/oauth/access_token", nil)
	if err != nil {
		return "", "", err
	}
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	req.Header.Set("Authorization", oauth1AuthorizationHeader(http.MethodPost, req.URL.String(), c.consumerKey, c.consumerSecret, requestToken, requestSecret, map[string]string{
		"oauth_token":    requestToken,
		"oauth_verifier": verifier,
	}))

	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", "", fmt.Errorf("status %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	vals, err := url.ParseQuery(string(body))
	if err != nil {
		return "", "", err
	}
	token = vals.Get("oauth_token")
	secret = vals.Get("oauth_token_secret")
	if token == "" || secret == "" {
		return "", "", fmt.Errorf("unexpected response: %s", strings.TrimSpace(string(body)))
	}
	return token, secret, nil
}

type Identity struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// Identity fetches the authenticated user's identity.
func (c *Client) Identity(ctx context.Context) (*Identity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/oauth/identity", nil)
	if err != nil {
		return nil, err
	}
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	req.Header.Set("Accept", "application/json; charset=utf-8")
	req.Header.Set("Authorization", oauth1AuthorizationHeader(http.MethodGet, req.URL.String(), c.consumerKey, c.consumerSecret, c.oauthToken, c.oauthSecret, nil))

	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("status %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	var out Identity
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- User collection types + methods ----

type Artist struct {
	Name string `json:"name"`
}

// Folder is a folder object inside a user collection.
type Folder struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Count       int    `json:"count"`
	ResourceURL string `json:"resource_url"`
}

type FolderList struct {
	Folders []*Folder `json:"folders"`
}

type ReleaseList struct {
	Pagination *Pagination          `json:"pagination"`
	Releases   []*CollectionRelease `json:"releases"`
}

// CollectionRelease is slightly different than a regular Release object.
type CollectionRelease struct {
	ID               int               `json:"id"`
	InstanceID        int               `json:"instance_id"`
	Rating            int               `json:"rating"`
	BasicInformation  *BasicInformation `json:"basic_information"`
	FolderID          int               `json:"folder_id"`
	DateAdded         string            `json:"date_added"`
	CollectionItemURL string            `json:"collection_item_url,omitempty"`
}

type BasicInformation struct {
	Title       string    `json:"title"`
	Year        int       `json:"year"`
	Thumb       string    `json:"thumb"`
	CoverImage  string    `json:"cover_image"`
	ResourceURL string    `json:"resource_url"`
	Artists     []*Artist `json:"artists"`
	Labels      []*Entity `json:"labels"`
	Formats     []*Format `json:"formats"`
	Genres      []string  `json:"genres"`
	Styles      []string  `json:"styles"`
	MasterID    int       `json:"master_id"`
	MasterURL   string    `json:"master_url"`
}

type Pagination struct {
	PerPage int           `json:"per_page"`
	Items   int           `json:"items"`
	Page    int           `json:"page"`
	URLs    PaginationURLs `json:"urls"`
	Pages   int           `json:"pages"`
}

type PaginationURLs struct {
	First    *string `json:"first"`
	Previous *string `json:"previous"`
	Next     *string `json:"next"`
	Last     *string `json:"last"`
}

func (c *Client) GetUserCollectionItemsByFolder(ctx context.Context, username, sort string, folderID, page, per int) (*ReleaseList, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/users/%s/collection/folders/%d/releases?page=%d&sort=%s&per_page=%d", c.baseURL, username, folderID, page, sort, per), nil)
	if err != nil {
		return nil, err
	}
	res := ReleaseList{}
	if err := c.sendRequest(req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *Client) GetUserCollectionAllItemsByFolder(ctx context.Context, username, sort string, folderID int) (*ReleaseList, error) {
	page := 1
	per := 100
	res := &ReleaseList{Releases: make([]*CollectionRelease, 0)}

	temp, err := c.GetUserCollectionItemsByFolder(ctx, username, sort, folderID, page, per)
	if err != nil {
		return nil, err
	}
	res.Releases = append(res.Releases, temp.Releases...)
	res.Pagination = temp.Pagination

	for temp.Pagination != nil && temp.Pagination.Pages > 1 && temp.Pagination.Page < temp.Pagination.Pages {
		nextPage := temp.Pagination.Page + 1
		temp, err = c.GetUserCollectionItemsByFolder(ctx, username, sort, folderID, nextPage, per)
		if err != nil {
			return nil, err
		}
		res.Releases = append(res.Releases, temp.Releases...)
	}
	return res, nil
}

// ---- Release types (subset) ----

type Release struct {
	ID               int              `json:"id"`
	Status           string           `json:"status"`
	Title            string           `json:"title"`
	Year             int              `json:"year"`
	ResourceURL      string           `json:"resource_url"`
	URI              string           `json:"uri"`
	Artists          []*ReleaseArtist  `json:"artists"`
	ArtistsSort      string           `json:"artists_sort"`
	Labels           []*Entity         `json:"labels"`
	Companies        []*Entity         `json:"companies"`
	Formats          []*Format         `json:"formats"`
	DataQuality      string           `json:"data_quality"`
	Community        Community         `json:"community"`
	FormatQuantity   int              `json:"format_quantity"`
	MasterID         int              `json:"master_id"`
	MasterURL        string           `json:"master_url"`
	Country          string           `json:"country"`
	Released         string           `json:"released"`
	ReleasedFormatted string          `json:"released_formatted"`
	Notes            string           `json:"notes"`
	Genres           []string          `json:"genres"`
	Styles           []string          `json:"styles"`
	Tracklist        []*Track          `json:"tracklist"`
	ExtraArtists     []*ReleaseArtist  `json:"extraartists"`
	Images           []*Image          `json:"images"`
	Thumb            string           `json:"thumb"`
}

type Image struct {
	Type        string `json:"type"`
	URI         string `json:"uri"`
	ResourceURL string `json:"resource_url"`
	URI150      string `json:"uri150"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
}

type Track struct {
	Title    string `json:"title"`
	Position string `json:"position"`
	Type     string `json:"type_"`
	Duration string `json:"duration"`
}

type Community struct {
	Have       int    `json:"have"`
	Want       int    `json:"want"`
	Rating     Rating `json:"rating"`
	DataQuality string `json:"data_quality"`
	Status     string `json:"status"`
}

type Rating struct {
	Count   int     `json:"count"`
	Average float32 `json:"average"`
}

type Format struct {
	Name         string   `json:"name"`
	Qty          string   `json:"qty"`
	Text         string   `json:"text"`
	Descriptions []string `json:"descriptions"`
}

type Entity struct {
	Name          string `json:"name"`
	Catno         string `json:"catno"`
	EntityType    string `json:"entity_type"`
	EntityTypeName string `json:"entity_type_name"`
	ID            int    `json:"id"`
	ResourceURL   string `json:"resource_url"`
}

type ReleaseArtist struct {
	Name        string `json:"name"`
	Anv         string `json:"anv"`
	Join        string `json:"join"`
	Role        string `json:"role"`
	Tracks      string `json:"tracks"`
	ID          int    `json:"id"`
	ResourceURL string `json:"resource_url"`
}

// GetRelease fetches a single release.
func (c *Client) GetRelease(ctx context.Context, id int, currency string) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/releases/%d?%s", c.baseURL, id, currency), nil)
	if err != nil {
		return nil, err
	}
	var out Release
	if err := c.sendRequest(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- Master (for "original year") ----

type Master struct {
	ID         int    `json:"id"`
	Title      string `json:"title"`
	Year       int    `json:"year"`
	ResourceURL string `json:"resource_url"`
	URI        string `json:"uri"`
}

func (c *Client) GetMasterRelease(ctx context.Context, id int) (*Master, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/masters/%d", c.baseURL, id), nil)
	if err != nil {
		return nil, err
	}
	var out Master
	if err := c.sendRequest(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- OAuth signing helpers ----

func oauth1AuthorizationHeader(method, rawURL, consumerKey, consumerSecret, token, tokenSecret string, extra map[string]string) string {
	params := map[string]string{
		"oauth_consumer_key":     consumerKey,
		"oauth_nonce":            oauthNonce(),
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        strconv.FormatInt(time.Now().Unix(), 10),
		"oauth_version":          "1.0",
	}
	if token != "" {
		params["oauth_token"] = token
	}
	for k, v := range extra {
		params[k] = v
	}

	baseString := oauthSignatureBaseString(method, rawURL, params)
	params["oauth_signature"] = oauthSignHMACSHA1(baseString, consumerSecret, tokenSecret)

	// Emit only oauth_* params.
	keys := make([]string, 0, len(params))
	for k := range params {
		if strings.HasPrefix(k, "oauth_") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("OAuth ")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(oauthPercentEncode(k))
		b.WriteString(`="`)
		b.WriteString(oauthPercentEncode(params[k]))
		b.WriteString(`"`)
	}
	return b.String()
}

func oauthSignatureBaseString(method, rawURL string, params map[string]string) string {
	u, _ := url.Parse(rawURL)
	query := u.Query()
	u.RawQuery = ""
	u.Fragment = ""

	type kv struct{ k, v string }
	kvs := make([]kv, 0, len(params)+len(query))
	for k, v := range params {
		if k == "oauth_signature" {
			continue
		}
		kvs = append(kvs, kv{k: oauthPercentEncode(k), v: oauthPercentEncode(v)})
	}
	for k, vs := range query {
		for _, v := range vs {
			kvs = append(kvs, kv{k: oauthPercentEncode(k), v: oauthPercentEncode(v)})
		}
	}
	sort.Slice(kvs, func(i, j int) bool {
		if kvs[i].k == kvs[j].k {
			return kvs[i].v < kvs[j].v
		}
		return kvs[i].k < kvs[j].k
	})

	var normalized strings.Builder
	for i, p := range kvs {
		if i > 0 {
			normalized.WriteByte('&')
		}
		normalized.WriteString(p.k)
		normalized.WriteByte('=')
		normalized.WriteString(p.v)
	}

	return strings.ToUpper(method) + "&" + oauthPercentEncode(u.String()) + "&" + oauthPercentEncode(normalized.String())
}

func oauthSignHMACSHA1(baseString, consumerSecret, tokenSecret string) string {
	key := oauthPercentEncode(consumerSecret) + "&" + oauthPercentEncode(tokenSecret)
	mac := hmac.New(sha1.New, []byte(key))
	_, _ = mac.Write([]byte(baseString))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func oauthNonce() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func oauthPercentEncode(s string) string {
	esc := url.QueryEscape(s)
	esc = strings.ReplaceAll(esc, "+", "%20")
	esc = strings.ReplaceAll(esc, "%7E", "~")
	return esc
}

