package main

import (
	"database/sql"
	"io/fs"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	githubOwner       = "purfect"
	githubRepo        = "Lesezeichen-Hub"
	moduleGithubOwner = "Lesezeichen-Hub"
	githubAPIBase     = "https://api.github.com"
	githubWebBase     = "https://github.com"
	githubRawBase     = "https://raw.githubusercontent.com"
	updateManifestURL = "https://raw.githubusercontent.com/purfect/Lesezeichen-Hub/main/version.json"
)

var appVersion = "dev"

type application struct {
	db                 *sql.DB
	webFS              fs.FS
	moduleAPIBase      string
	moduleWebBase      string
	moduleManifestBase string
	moduleInstallDir   string
	externalPrices     bool
	metalPricesMu      sync.RWMutex
	metalPrices        metalPricesPayload
	metalPricesAt      time.Time
	metalPricesErr     string
	silverPricesMu     sync.RWMutex
	silverPrices       silverPricesPayload
	silverPricesAt     time.Time
}

type metalPricesPayload struct {
	GoldEURPerGram   float64   `json:"gold_eur_per_g"`
	SilverEURPerGram float64   `json:"silver_eur_per_g"`
	FetchedAt        time.Time `json:"fetched_at"`
	Cached           bool      `json:"cached"`
	Stale            bool      `json:"stale"`
	LastError        string    `json:"last_error,omitempty"`
}

type silverProduct struct {
	Name      string  `json:"name"`
	Category  string  `json:"category"`
	Price     float64 `json:"price"`
	PriceText string  `json:"price_text"`
	URL       string  `json:"url"`
}

type silverPricesPayload struct {
	BestProduct *silverProduct  `json:"best_product"`
	AllProducts []silverProduct `json:"all_products"`
	FetchedAt   time.Time       `json:"fetched_at"`
	Source      string          `json:"source"`
}

type silverPriceHistoryEntry struct {
	FetchedAt       time.Time `json:"fetched_at"`
	EURPerGram      float64   `json:"eur_per_g"`
	BestEURPerOunce float64   `json:"best_eur_per_ounce"`
	BestProductName string    `json:"best_product_name"`
	BestProductURL  string    `json:"best_product_url"`
}

type silverPriceHistoryBounds struct {
	Earliest string `json:"earliest"`
	Latest   string `json:"latest"`
}

type group struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	SortOrder   int        `json:"sort_order"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	Bookmarks   []bookmark `json:"bookmarks,omitempty"`
}

type bookmark struct {
	ID         int64      `json:"id"`
	GroupID    int64      `json:"group_id"`
	Title      string     `json:"title"`
	URL        string     `json:"url"`
	Notes      string     `json:"notes"`
	Tags       []string   `json:"tags"`
	Favorite   bool       `json:"favorite"`
	Pinned     bool       `json:"pinned"`
	Archived   bool       `json:"archived"`
	SortOrder  int        `json:"sort_order"`
	OpenCount  int        `json:"open_count"`
	LastOpened *time.Time `json:"last_opened_at,omitempty"`
	RemindAt   *time.Time `json:"remind_at,omitempty"`
	CreatedAt  *time.Time `json:"created_at,omitempty"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
}

type note struct {
	ID          int64      `json:"id"`
	Title       string     `json:"title"`
	Content     string     `json:"content"`
	Type        string     `json:"type"`
	GroupID     *int64     `json:"group_id,omitempty"`
	BookmarkIDs []int64    `json:"bookmark_ids"`
	Tags        []string   `json:"tags"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
}

type noteGroup struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	SortOrder int        `json:"sort_order"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
}

type localModule struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	Path             string `json:"path"`
	URL              string `json:"url"`
	SourceURL        string `json:"source_url,omitempty"`
	InstalledVersion string `json:"installed_version,omitempty"`
	Available        bool   `json:"available"`
	Managed          bool   `json:"managed"`
	Error            string `json:"error,omitempty"`
}

type catalogModule struct {
	Name             string `json:"name"`
	Category         string `json:"category"`
	Description      string `json:"description"`
	RepositoryURL    string `json:"repository_url"`
	DefaultBranch    string `json:"default_branch"`
	Version          string `json:"version,omitempty"`
	InstalledVersion string `json:"installed_version,omitempty"`
	UpdateAvailable  bool   `json:"update_available"`
	Installed        bool   `json:"installed"`
	LocalID          int64  `json:"local_id,omitempty"`
	LocalURL         string `json:"local_url,omitempty"`
}

type githubRepository struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Topics        []string `json:"topics"`
	HTMLURL       string   `json:"html_url"`
	DefaultBranch string   `json:"default_branch"`
	Archived      bool     `json:"archived"`
	Fork          bool     `json:"fork"`
}

type apiError struct {
	Error string `json:"error"`
}

type updateInfo struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	AssetName       string `json:"asset_name,omitempty"`
	ReleaseURL      string `json:"release_url,omitempty"`
	AssetURL        string `json:"asset_url,omitempty"`
	ChecksumURL     string `json:"checksum_url,omitempty"`
	CanInstall      bool   `json:"can_install"`
	Message         string `json:"message,omitempty"`
}

type githubRelease struct {
	TagName string               `json:"tag_name"`
	HTMLURL string               `json:"html_url"`
	Assets  []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type updateManifest struct {
	LatestVersion string `json:"latest_version"`
	AssetName     string `json:"asset_name"`
	AssetURL      string `json:"asset_url"`
	ChecksumURL   string `json:"checksum_url"`
	ReleaseURL    string `json:"release_url"`
}

type moduleVersionManifest struct {
	Version string `json:"version"`
}

type importPayload struct {
	Groups []importGroup `json:"groups"`
}

type importGroup struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	SortOrder   int              `json:"sort_order"`
	Bookmarks   []importBookmark `json:"bookmarks"`
}

type importBookmark struct {
	Title     string   `json:"title"`
	URL       string   `json:"url"`
	Notes     string   `json:"notes"`
	Tags      []string `json:"tags"`
	Favorite  bool     `json:"favorite"`
	Pinned    bool     `json:"pinned"`
	Archived  bool     `json:"archived"`
	SortOrder int      `json:"sort_order"`
	RemindAt  string   `json:"remind_at"`
}

type speedDialImport struct {
	Dials []speedDialDial `json:"dials"`
}

type speedDialDial struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type backupPayload struct {
	Version    int           `json:"version"`
	ExportedAt time.Time     `json:"exported_at"`
	Groups     []backupGroup `json:"groups"`
	Notes      []backupNote  `json:"notes"`
}

type backupGroup struct {
	ID          int64            `json:"id"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	SortOrder   int              `json:"sort_order"`
	Bookmarks   []backupBookmark `json:"bookmarks"`
}

type backupBookmark struct {
	ID        int64      `json:"id"`
	Title     string     `json:"title"`
	URL       string     `json:"url"`
	Notes     string     `json:"notes"`
	Tags      []string   `json:"tags"`
	Favorite  bool       `json:"favorite"`
	Pinned    bool       `json:"pinned"`
	Archived  bool       `json:"archived"`
	SortOrder int        `json:"sort_order"`
	RemindAt  *time.Time `json:"remind_at,omitempty"`
}

type backupNote struct {
	Title       string   `json:"title"`
	Content     string   `json:"content"`
	Type        string   `json:"type"`
	BookmarkIDs []int64  `json:"bookmark_ids"`
	Tags        []string `json:"tags"`
}

type restorePreview struct {
	Valid                bool     `json:"valid"`
	Errors               []string `json:"errors"`
	NewGroups            int      `json:"new_groups"`
	ExistingGroups       int      `json:"existing_groups"`
	NewBookmarks         int      `json:"new_bookmarks"`
	ConflictingBookmarks int      `json:"conflicting_bookmarks"`
	NewNotes             int      `json:"new_notes"`
	ConflictingNotes     int      `json:"conflicting_notes"`
	Conflicts            []string `json:"conflicts"`
}
