package navigation

import (
	"os"
	"sync"
	"time"

	"github.com/arazzo/lsp/utils"
)

// OperationIndex stores the mapping of operationIds to their definitions
type OperationIndex struct {
	Operations map[string]*OperationInfo
	Channels   map[string]*ChannelInfo // AsyncAPI channels, keyed by channel key (e.g. "orders")
	Files      map[string]*OpenAPIFile
	mutex      sync.RWMutex
}

// ChannelInfo contains information about an AsyncAPI channel definition (for navigation).
type ChannelInfo struct {
	Key        string // the channel's key under `channels:` (e.g. "orders")
	Address    string // the channel `address` (broker-side name, e.g. "orders/new")
	FileURI    string
	FileName   string
	LineNumber int
}

// OperationInfo contains information about an OpenAPI operation
type OperationInfo struct {
	OperationID string
	Method      string   // GET, POST, PUT, DELETE, etc.
	Path        string   // /pets/{petId}
	Summary     string
	Description string
	FileURI     string
	FileName    string   // Base filename for display
	LineNumber  int
	Column      int
	Tags        []string
}

// OpenAPIFile represents a parsed OpenAPI or AsyncAPI specification file
type OpenAPIFile struct {
	URI        string
	Version    string
	Title      string
	Description string
	Operations []*OperationInfo
	Channels   []*ChannelInfo // AsyncAPI channels (empty for OpenAPI files)
	ParsedAt   time.Time
}

// NewOperationIndex creates a new operation index
func NewOperationIndex() *OperationIndex {
	return &OperationIndex{
		Operations: make(map[string]*OperationInfo),
		Channels:   make(map[string]*ChannelInfo),
		Files:      make(map[string]*OpenAPIFile),
	}
}

// AddChannel adds an AsyncAPI channel to the index (thread-safe, keeps the first occurrence).
func (idx *OperationIndex) AddChannel(ch *ChannelInfo) {
	idx.mutex.Lock()
	defer idx.mutex.Unlock()
	if _, exists := idx.Channels[ch.Key]; exists {
		return
	}
	idx.Channels[ch.Key] = ch
}

// LookupChannel finds a channel by its key (thread-safe).
func (idx *OperationIndex) LookupChannel(key string) (*ChannelInfo, bool) {
	idx.mutex.RLock()
	defer idx.mutex.RUnlock()
	ch, found := idx.Channels[key]
	return ch, found
}

// ChannelCount returns the number of indexed channels (thread-safe).
func (idx *OperationIndex) ChannelCount() int {
	idx.mutex.RLock()
	defer idx.mutex.RUnlock()
	return len(idx.Channels)
}

// AddOperation adds an operation to the index (thread-safe)
func (idx *OperationIndex) AddOperation(op *OperationInfo) {
	idx.mutex.Lock()
	defer idx.mutex.Unlock()

	// Check for duplicates
	if existing, exists := idx.Operations[op.OperationID]; exists {
		// Log warning about duplicate
		// For now, we keep the first occurrence
		_ = existing
		return
	}

	idx.Operations[op.OperationID] = op
}

// AddFile adds a parsed OpenAPI file to the index
func (idx *OperationIndex) AddFile(file *OpenAPIFile) {
	idx.mutex.Lock()
	defer idx.mutex.Unlock()

	idx.Files[file.URI] = file
}

// Lookup finds an operation by its operationId (thread-safe)
func (idx *OperationIndex) Lookup(operationID string) (*OperationInfo, bool) {
	idx.mutex.RLock()
	defer idx.mutex.RUnlock()

	op, found := idx.Operations[operationID]
	return op, found
}

// RemoveFile removes all operations from a file (thread-safe)
func (idx *OperationIndex) RemoveFile(fileURI string) {
	idx.mutex.Lock()
	defer idx.mutex.Unlock()

	// Remove file
	delete(idx.Files, fileURI)

	// Remove all operations from this file
	for opID, op := range idx.Operations {
		if op.FileURI == fileURI {
			delete(idx.Operations, opID)
		}
	}

	// Remove all channels from this file
	for key, ch := range idx.Channels {
		if ch.FileURI == fileURI {
			delete(idx.Channels, key)
		}
	}
}

// ListAll returns all operations (thread-safe)
func (idx *OperationIndex) ListAll() []*OperationInfo {
	idx.mutex.RLock()
	defer idx.mutex.RUnlock()

	ops := make([]*OperationInfo, 0, len(idx.Operations))
	for _, op := range idx.Operations {
		ops = append(ops, op)
	}

	return ops
}

// Count returns the number of indexed operations
func (idx *OperationIndex) Count() int {
	idx.mutex.RLock()
	defer idx.mutex.RUnlock()

	return len(idx.Operations)
}

// Clear removes all entries from the index
func (idx *OperationIndex) Clear() {
	idx.mutex.Lock()
	defer idx.mutex.Unlock()

	idx.Operations = make(map[string]*OperationInfo)
	idx.Channels = make(map[string]*ChannelInfo)
	idx.Files = make(map[string]*OpenAPIFile)
}

// FileCache provides caching for parsed OpenAPI files with TTL
type FileCache struct {
	entries map[string]*CacheEntry
	mutex   sync.RWMutex
	ttl     time.Duration
}

// CacheEntry represents a cached file with metadata
type CacheEntry struct {
	File      *OpenAPIFile
	ModTime   time.Time
	CachedAt  time.Time
	HitCount  int
}

// NewFileCache creates a new file cache with the given TTL
func NewFileCache(ttl time.Duration) *FileCache {
	return &FileCache{
		entries: make(map[string]*CacheEntry),
		ttl:     ttl,
	}
}

// Get retrieves a file from cache if valid
func (fc *FileCache) Get(fileURI string) (*OpenAPIFile, bool) {
	fc.mutex.RLock()
	defer fc.mutex.RUnlock()

	entry, exists := fc.entries[fileURI]
	if !exists {
		return nil, false
	}

	// Check if cache entry is still valid (TTL not expired)
	if time.Since(entry.CachedAt) > fc.ttl {
		return nil, false
	}

	// Check if file has been modified since caching
	filePath, err := utils.URIToPath(fileURI)
	if err != nil {
		return nil, false
	}
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		// File might have been deleted
		return nil, false
	}

	if !fileInfo.ModTime().Equal(entry.ModTime) {
		// File has been modified
		return nil, false
	}

	// Cache hit - update hit count
	entry.HitCount++
	return entry.File, true
}

// Put adds or updates a file in the cache
func (fc *FileCache) Put(fileURI string, file *OpenAPIFile) error {
	fc.mutex.Lock()
	defer fc.mutex.Unlock()

	// Get file modification time
	filePath, err := utils.URIToPath(fileURI)
	if err != nil {
		return err
	}
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return err
	}

	fc.entries[fileURI] = &CacheEntry{
		File:     file,
		ModTime:  fileInfo.ModTime(),
		CachedAt: time.Now(),
		HitCount: 0,
	}

	return nil
}

// Invalidate removes a file from the cache
func (fc *FileCache) Invalidate(fileURI string) {
	fc.mutex.Lock()
	defer fc.mutex.Unlock()

	delete(fc.entries, fileURI)
}

// Clear removes all entries from the cache
func (fc *FileCache) Clear() {
	fc.mutex.Lock()
	defer fc.mutex.Unlock()

	fc.entries = make(map[string]*CacheEntry)
}

// Stats returns cache statistics
func (fc *FileCache) Stats() (entries int, totalHits int) {
	fc.mutex.RLock()
	defer fc.mutex.RUnlock()

	entries = len(fc.entries)
	for _, entry := range fc.entries {
		totalHits += entry.HitCount
	}

	return entries, totalHits
}

// CleanExpired removes expired entries from the cache
func (fc *FileCache) CleanExpired() int {
	fc.mutex.Lock()
	defer fc.mutex.Unlock()

	removed := 0
	for uri, entry := range fc.entries {
		if time.Since(entry.CachedAt) > fc.ttl {
			delete(fc.entries, uri)
			removed++
		}
	}

	return removed
}
