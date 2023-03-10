// SPDX-License-Identifier: AGPL-3.0-only
// Provenance-includes-location: https://github.com/cortexproject/cortex/blob/master/pkg/storage/tsdb/config.go
// Provenance-includes-license: Apache-2.0
// Provenance-includes-copyright: The Cortex Authors.

package tsdb

import (
	"flag"
	"path/filepath"
	"strings"
	"time"

	"github.com/alecthomas/units"
	"github.com/go-kit/log"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/tsdb/chunks"
	"github.com/prometheus/prometheus/tsdb/wlog"

	"github.com/grafana/mimir/pkg/storage/bucket"
	"github.com/grafana/mimir/pkg/storegateway/indexheader"
	"github.com/grafana/mimir/pkg/util"
)

const (
	// DeprecatedTenantIDExternalLabel is the external label containing the tenant ID,
	// set when shipping blocks to the storage.
	//
	// Mimir no longer generates blocks with this label, however old blocks may still use this label.
	DeprecatedTenantIDExternalLabel = "__org_id__"

	// DeprecatedIngesterIDExternalLabel is the external label containing the ingester ID,
	// set when shipping blocks to the storage.
	//
	// Mimir no longer generates blocks with this label, however old blocks may still use this label.
	DeprecatedIngesterIDExternalLabel = "__ingester_id__"

	// CompactorShardIDExternalLabel is the external label used to store
	// the ID of a sharded block generated by the split-and-merge compactor. If a block hasn't
	// this label, it means the block hasn't been split.
	CompactorShardIDExternalLabel = "__compactor_shard_id__"

	// DeprecatedShardIDExternalLabel is deprecated.
	DeprecatedShardIDExternalLabel = "__shard_id__"

	// OutOfOrderExternalLabel is the external label used to mark blocks
	// containing out-of-order data.
	OutOfOrderExternalLabel = "__out_of_order__"

	// OutOfOrderExternalLabelValue is the value to be used for the OutOfOrderExternalLabel label
	OutOfOrderExternalLabelValue = "true"

	// DefaultCloseIdleTSDBInterval is how often are open TSDBs checked for being idle and closed.
	DefaultCloseIdleTSDBInterval = 5 * time.Minute

	// DeletionMarkCheckInterval is how often to check for tenant deletion mark.
	DeletionMarkCheckInterval = 1 * time.Hour

	// EstimatedMaxChunkSize is average max of chunk size. This can be exceeded though in very rare (valid) cases.
	EstimatedMaxChunkSize = 16000

	// ChunkPoolDefaultMinBucketSize is the default minimum bucket size (bytes) of the chunk pool.
	ChunkPoolDefaultMinBucketSize = EstimatedMaxChunkSize

	// ChunkPoolDefaultMaxBucketSize is the default maximum bucket size (bytes) of the chunk pool.
	ChunkPoolDefaultMaxBucketSize = 50e6

	// DefaultPostingOffsetInMemorySampling represents default value for --store.index-header-posting-offsets-in-mem-sampling.
	// 32 value is chosen as it's a good balance for common setups. Sampling that is not too large (too many CPU cycles) and
	// not too small (too much memory).
	DefaultPostingOffsetInMemorySampling = 32

	// DefaultPartitionerMaxGapSize is the default max size - in bytes - of a gap for which the store-gateway
	// partitioner aggregates together two bucket GET object requests.
	DefaultPartitionerMaxGapSize = uint64(512 * 1024)

	headChunkWriterBufferSizeHelp        = "The write buffer size used by the head chunks mapper. Lower values reduce memory utilisation on clusters with a large number of tenants at the cost of increased disk I/O operations."
	headChunksEndTimeVarianceHelp        = "How much variance (as percentage between 0 and 1) should be applied to the chunk end time, to spread chunks writing across time. Doesn't apply to the last chunk of the chunk range. 0 means no variance."
	headStripeSizeHelp                   = "The number of shards of series to use in TSDB (must be a power of 2). Reducing this will decrease memory footprint, but can negatively impact performance."
	headChunksWriteQueueSizeHelp         = "The size of the write queue used by the head chunks mapper. Lower values reduce memory utilisation at the cost of potentially higher ingest latency. Value of 0 switches chunks mapper to implementation without a queue."
	headPostingsForMatchersCacheTTLHelp  = "How long to cache postings for matchers in the Head and OOOHead. 0 disables the cache and just deduplicates the in-flight calls."
	headPostingsForMatchersCacheSizeHelp = "Maximum number of entries in the cache for postings for matchers in the Head and OOOHead when ttl > 0."
	headPostingsForMatchersCacheForce    = "Force the cache to be used for postings for matchers in the Head and OOOHead, even if it's not a concurrent (query-sharding) call."

	consistencyDelayFlag = "blocks-storage.bucket-store.consistency-delay"
)

// Validation errors
var (
	errInvalidShipConcurrency       = errors.New("invalid TSDB ship concurrency")
	errInvalidOpeningConcurrency    = errors.New("invalid TSDB opening concurrency")
	errInvalidCompactionInterval    = errors.New("invalid TSDB compaction interval")
	errInvalidCompactionConcurrency = errors.New("invalid TSDB compaction concurrency")
	errInvalidWALSegmentSizeBytes   = errors.New("invalid TSDB WAL segment size bytes")
	errInvalidStripeSize            = errors.New("invalid TSDB stripe size")
	errInvalidStreamingBatchSize    = errors.New("invalid store-gateway streaming batch size")
	errEmptyBlockranges             = errors.New("empty block ranges for TSDB")
)

// BlocksStorageConfig holds the config information for the blocks storage.
type BlocksStorageConfig struct {
	Bucket      bucket.Config     `yaml:",inline"`
	BucketStore BucketStoreConfig `yaml:"bucket_store" doc:"description=This configures how the querier and store-gateway discover and synchronize blocks stored in the bucket."`
	TSDB        TSDBConfig        `yaml:"tsdb"`
}

// DurationList is the block ranges for a tsdb
type DurationList []time.Duration

// String implements the flag.Value interface
func (d *DurationList) String() string {
	values := make([]string, 0, len(*d))
	for _, v := range *d {
		values = append(values, v.String())
	}

	return strings.Join(values, ",")
}

// Set implements the flag.Value interface
func (d *DurationList) Set(s string) error {
	values := strings.Split(s, ",")
	*d = make([]time.Duration, 0, len(values)) // flag.Parse may be called twice, so overwrite instead of append
	for _, v := range values {
		t, err := time.ParseDuration(v)
		if err != nil {
			return err
		}
		*d = append(*d, t)
	}
	return nil
}

// ToMilliseconds returns the duration list in milliseconds
func (d *DurationList) ToMilliseconds() []int64 {
	values := make([]int64, 0, len(*d))
	for _, t := range *d {
		values = append(values, t.Milliseconds())
	}

	return values
}

// RegisterFlags registers the TSDB flags
func (cfg *BlocksStorageConfig) RegisterFlags(f *flag.FlagSet, logger log.Logger) {
	cfg.Bucket.RegisterFlagsWithPrefixAndDefaultDirectory("blocks-storage.", "blocks", f, logger)
	cfg.BucketStore.RegisterFlags(f, logger)
	cfg.TSDB.RegisterFlags(f)
}

// Validate the config.
func (cfg *BlocksStorageConfig) Validate(logger log.Logger) error {
	if err := cfg.Bucket.Validate(); err != nil {
		return err
	}

	if err := cfg.TSDB.Validate(); err != nil {
		return err
	}

	return cfg.BucketStore.Validate(logger)
}

// TSDBConfig holds the config for TSDB opened in the ingesters.
//
//nolint:revive
type TSDBConfig struct {
	Dir                       string        `yaml:"dir"`
	BlockRanges               DurationList  `yaml:"block_ranges_period" category:"experimental" doc:"hidden"`
	Retention                 time.Duration `yaml:"retention_period"`
	ShipInterval              time.Duration `yaml:"ship_interval" category:"advanced"`
	ShipConcurrency           int           `yaml:"ship_concurrency" category:"advanced"`
	HeadCompactionInterval    time.Duration `yaml:"head_compaction_interval" category:"advanced"`
	HeadCompactionConcurrency int           `yaml:"head_compaction_concurrency" category:"advanced"`
	HeadCompactionIdleTimeout time.Duration `yaml:"head_compaction_idle_timeout" category:"advanced"`
	HeadChunksWriteBufferSize int           `yaml:"head_chunks_write_buffer_size_bytes" category:"advanced"`
	HeadChunksEndTimeVariance float64       `yaml:"head_chunks_end_time_variance" category:"experimental"`
	StripeSize                int           `yaml:"stripe_size" category:"advanced"`
	WALCompressionEnabled     bool          `yaml:"wal_compression_enabled" category:"advanced"`
	WALSegmentSizeBytes       int           `yaml:"wal_segment_size_bytes" category:"advanced"`
	FlushBlocksOnShutdown     bool          `yaml:"flush_blocks_on_shutdown" category:"advanced"`
	CloseIdleTSDBTimeout      time.Duration `yaml:"close_idle_tsdb_timeout" category:"advanced"`
	MemorySnapshotOnShutdown  bool          `yaml:"memory_snapshot_on_shutdown" category:"experimental"`
	HeadChunksWriteQueueSize  int           `yaml:"head_chunks_write_queue_size" category:"advanced"`

	// Series hash cache.
	SeriesHashCacheMaxBytes uint64 `yaml:"series_hash_cache_max_size_bytes" category:"advanced"`

	// MaxTSDBOpeningConcurrencyOnStartup limits the number of concurrently opening TSDB's during startup.
	MaxTSDBOpeningConcurrencyOnStartup int `yaml:"max_tsdb_opening_concurrency_on_startup" category:"advanced"`

	// If true, user TSDBs are not closed on shutdown. Only for testing.
	// If false (default), user TSDBs are closed to make sure all resources are released and closed properly.
	KeepUserTSDBOpenOnShutdown bool `yaml:"-"`

	// How often to check for idle TSDBs for closing. DefaultCloseIdleTSDBInterval is not suitable for testing, so tests can override.
	CloseIdleTSDBInterval time.Duration `yaml:"-"`

	// For experimental out of order metrics support.
	OutOfOrderCapacityMax int `yaml:"out_of_order_capacity_max" category:"experimental"`

	// HeadPostingsForMatchersCacheTTL is the TTL of the postings for matchers cache in the Head.
	// If it's 0, the cache will only deduplicate in-flight requests, deleting the results once the first request has finished.
	HeadPostingsForMatchersCacheTTL time.Duration `yaml:"head_postings_for_matchers_cache_ttl" category:"experimental"`
	// HeadPostingsForMatchersCacheSize is the maximum size of cached postings for matchers elements in the Head.
	// It's ignored used when HeadPostingsForMatchersCacheTTL is 0.
	HeadPostingsForMatchersCacheSize int `yaml:"head_postings_for_matchers_cache_size" category:"experimental"`
	// HeadPostingsForMatchersCacheForce forces the usage of postings for matchers cache for all calls on Head and OOOHead regardless of the `concurrent` param.
	HeadPostingsForMatchersCacheForce bool `yaml:"head_postings_for_matchers_cache_force" category:"experimental"`
}

// RegisterFlags registers the TSDBConfig flags.
func (cfg *TSDBConfig) RegisterFlags(f *flag.FlagSet) {
	if len(cfg.BlockRanges) == 0 {
		cfg.BlockRanges = []time.Duration{2 * time.Hour} // Default 2h block
	}

	f.StringVar(&cfg.Dir, "blocks-storage.tsdb.dir", "./tsdb/", "Directory to store TSDBs (including WAL) in the ingesters. This directory is required to be persisted between restarts.")
	f.Var(&cfg.BlockRanges, "blocks-storage.tsdb.block-ranges-period", "TSDB blocks range period.")
	f.DurationVar(&cfg.Retention, "blocks-storage.tsdb.retention-period", 13*time.Hour, "TSDB blocks retention in the ingester before a block is removed. If shipping is enabled, the retention will be relative to the time when the block was uploaded to storage. If shipping is disabled then its relative to the creation time of the block. This should be larger than the -blocks-storage.tsdb.block-ranges-period, -querier.query-store-after and large enough to give store-gateways and queriers enough time to discover newly uploaded blocks.")
	f.DurationVar(&cfg.ShipInterval, "blocks-storage.tsdb.ship-interval", 1*time.Minute, "How frequently the TSDB blocks are scanned and new ones are shipped to the storage. 0 means shipping is disabled.")
	f.IntVar(&cfg.ShipConcurrency, "blocks-storage.tsdb.ship-concurrency", 10, "Maximum number of tenants concurrently shipping blocks to the storage.")
	f.Uint64Var(&cfg.SeriesHashCacheMaxBytes, "blocks-storage.tsdb.series-hash-cache-max-size-bytes", uint64(1*units.Gibibyte), "Max size - in bytes - of the in-memory series hash cache. The cache is shared across all tenants and it's used only when query sharding is enabled.")
	f.IntVar(&cfg.MaxTSDBOpeningConcurrencyOnStartup, "blocks-storage.tsdb.max-tsdb-opening-concurrency-on-startup", 10, "limit the number of concurrently opening TSDB's on startup")
	f.DurationVar(&cfg.HeadCompactionInterval, "blocks-storage.tsdb.head-compaction-interval", 1*time.Minute, "How frequently the ingester checks whether the TSDB head should be compacted and, if so, triggers the compaction. Mimir applies a jitter to the first check, while subsequent checks will happen at the configured interval. Block is only created if data covers smallest block range. The configured interval must be between 0 and 15 minutes.")
	f.IntVar(&cfg.HeadCompactionConcurrency, "blocks-storage.tsdb.head-compaction-concurrency", 1, "Maximum number of tenants concurrently compacting TSDB head into a new block")
	f.DurationVar(&cfg.HeadCompactionIdleTimeout, "blocks-storage.tsdb.head-compaction-idle-timeout", 1*time.Hour, "If TSDB head is idle for this duration, it is compacted. Note that up to 25% jitter is added to the value to avoid ingesters compacting concurrently. 0 means disabled.")
	f.IntVar(&cfg.HeadChunksWriteBufferSize, "blocks-storage.tsdb.head-chunks-write-buffer-size-bytes", chunks.DefaultWriteBufferSize, headChunkWriterBufferSizeHelp)
	f.Float64Var(&cfg.HeadChunksEndTimeVariance, "blocks-storage.tsdb.head-chunks-end-time-variance", 0, headChunksEndTimeVarianceHelp)
	f.IntVar(&cfg.StripeSize, "blocks-storage.tsdb.stripe-size", 16384, headStripeSizeHelp)
	f.BoolVar(&cfg.WALCompressionEnabled, "blocks-storage.tsdb.wal-compression-enabled", false, "True to enable TSDB WAL compression.")
	f.IntVar(&cfg.WALSegmentSizeBytes, "blocks-storage.tsdb.wal-segment-size-bytes", wlog.DefaultSegmentSize, "TSDB WAL segments files max size (bytes).")
	f.BoolVar(&cfg.FlushBlocksOnShutdown, "blocks-storage.tsdb.flush-blocks-on-shutdown", false, "True to flush blocks to storage on shutdown. If false, incomplete blocks will be reused after restart.")
	f.DurationVar(&cfg.CloseIdleTSDBTimeout, "blocks-storage.tsdb.close-idle-tsdb-timeout", 13*time.Hour, "If TSDB has not received any data for this duration, and all blocks from TSDB have been shipped, TSDB is closed and deleted from local disk. If set to positive value, this value should be equal or higher than -querier.query-ingesters-within flag to make sure that TSDB is not closed prematurely, which could cause partial query results. 0 or negative value disables closing of idle TSDB.")
	f.BoolVar(&cfg.MemorySnapshotOnShutdown, "blocks-storage.tsdb.memory-snapshot-on-shutdown", false, "True to enable snapshotting of in-memory TSDB data on disk when shutting down.")
	f.IntVar(&cfg.HeadChunksWriteQueueSize, "blocks-storage.tsdb.head-chunks-write-queue-size", 1000000, headChunksWriteQueueSizeHelp)
	f.IntVar(&cfg.OutOfOrderCapacityMax, "blocks-storage.tsdb.out-of-order-capacity-max", 32, "Maximum capacity for out of order chunks, in samples between 1 and 255.")
	f.DurationVar(&cfg.HeadPostingsForMatchersCacheTTL, "blocks-storage.tsdb.head-postings-for-matchers-cache-ttl", 10*time.Second, headPostingsForMatchersCacheTTLHelp)
	f.IntVar(&cfg.HeadPostingsForMatchersCacheSize, "blocks-storage.tsdb.head-postings-for-matchers-cache-size", 100, headPostingsForMatchersCacheSizeHelp)
	f.BoolVar(&cfg.HeadPostingsForMatchersCacheForce, "blocks-storage.tsdb.head-postings-for-matchers-cache-force", false, headPostingsForMatchersCacheForce)
}

// Validate the config.
func (cfg *TSDBConfig) Validate() error {
	if cfg.ShipInterval > 0 && cfg.ShipConcurrency <= 0 {
		return errInvalidShipConcurrency
	}

	if cfg.MaxTSDBOpeningConcurrencyOnStartup <= 0 {
		return errInvalidOpeningConcurrency
	}

	if cfg.HeadCompactionInterval <= 0 || cfg.HeadCompactionInterval > 15*time.Minute {
		return errInvalidCompactionInterval
	}

	if cfg.HeadCompactionConcurrency <= 0 {
		return errInvalidCompactionConcurrency
	}

	if cfg.HeadChunksWriteBufferSize < chunks.MinWriteBufferSize || cfg.HeadChunksWriteBufferSize > chunks.MaxWriteBufferSize || cfg.HeadChunksWriteBufferSize%1024 != 0 {
		return errors.Errorf("head chunks write buffer size must be a multiple of 1024 between %d and %d", chunks.MinWriteBufferSize, chunks.MaxWriteBufferSize)
	}

	if cfg.StripeSize <= 1 || (cfg.StripeSize&(cfg.StripeSize-1)) != 0 { // ensure stripe size is a positive power of 2
		return errInvalidStripeSize
	}

	if len(cfg.BlockRanges) == 0 {
		return errEmptyBlockranges
	}

	if cfg.WALSegmentSizeBytes <= 0 {
		return errInvalidWALSegmentSizeBytes
	}

	return nil
}

// BlocksDir returns the directory path where TSDB blocks and wal should be
// stored by the ingester
func (cfg *TSDBConfig) BlocksDir(userID string) string {
	return filepath.Join(cfg.Dir, userID)
}

// IsShippingEnabled returns whether blocks shipping is enabled.
func (cfg *TSDBConfig) IsBlocksShippingEnabled() bool {
	return cfg.ShipInterval > 0
}

// BucketStoreConfig holds the config information for Bucket Stores used by the querier and store-gateway.
type BucketStoreConfig struct {
	SyncDir                    string              `yaml:"sync_dir"`
	SyncInterval               time.Duration       `yaml:"sync_interval" category:"advanced"`
	MaxConcurrent              int                 `yaml:"max_concurrent" category:"advanced"`
	TenantSyncConcurrency      int                 `yaml:"tenant_sync_concurrency" category:"advanced"`
	BlockSyncConcurrency       int                 `yaml:"block_sync_concurrency" category:"advanced"`
	MetaSyncConcurrency        int                 `yaml:"meta_sync_concurrency" category:"advanced"`
	DeprecatedConsistencyDelay time.Duration       `yaml:"consistency_delay" category:"deprecated"` // Deprecated. Remove in Mimir 2.9.
	IndexCache                 IndexCacheConfig    `yaml:"index_cache"`
	ChunksCache                ChunksCacheConfig   `yaml:"chunks_cache"`
	MetadataCache              MetadataCacheConfig `yaml:"metadata_cache"`
	IgnoreDeletionMarksDelay   time.Duration       `yaml:"ignore_deletion_mark_delay" category:"advanced"`
	BucketIndex                BucketIndexConfig   `yaml:"bucket_index"`
	IgnoreBlocksWithin         time.Duration       `yaml:"ignore_blocks_within" category:"advanced"`

	// Chunk pool.
	MaxChunkPoolBytes           uint64 `yaml:"max_chunk_pool_bytes" category:"advanced"`
	ChunkPoolMinBucketSizeBytes int    `yaml:"chunk_pool_min_bucket_size_bytes" category:"advanced"`
	ChunkPoolMaxBucketSizeBytes int    `yaml:"chunk_pool_max_bucket_size_bytes" category:"advanced"`

	// Series hash cache.
	SeriesHashCacheMaxBytes uint64 `yaml:"series_hash_cache_max_size_bytes" category:"advanced"`

	// Controls whether index-header lazy loading is enabled.
	IndexHeaderLazyLoadingEnabled     bool          `yaml:"index_header_lazy_loading_enabled" category:"advanced"`
	IndexHeaderLazyLoadingIdleTimeout time.Duration `yaml:"index_header_lazy_loading_idle_timeout" category:"advanced"`

	// Controls the partitioner, used to aggregate multiple GET object API requests.
	PartitionerMaxGapBytes uint64 `yaml:"partitioner_max_gap_bytes" category:"advanced"`

	// Controls what is the ratio of postings offsets store will hold in memory.
	// Larger value will keep less offsets, which will increase CPU cycles needed for query touching those postings.
	// It's meant for setups that want low baseline memory pressure and where less traffic is expected.
	// On the contrary, smaller value will increase baseline memory usage, but improve latency slightly.
	// 1 will keep all in memory. Default value is the same as in Prometheus which gives a good balance.
	PostingOffsetsInMemSampling int `yaml:"postings_offsets_in_mem_sampling" category:"advanced"`

	// Controls experimental options for index-header file reading.
	IndexHeader indexheader.Config `yaml:"index_header" category:"experimental"`

	StreamingBatchSize int `yaml:"streaming_series_batch_size" category:"advanced"`
}

// RegisterFlags registers the BucketStore flags
func (cfg *BucketStoreConfig) RegisterFlags(f *flag.FlagSet, logger log.Logger) {
	cfg.IndexCache.RegisterFlagsWithPrefix(f, "blocks-storage.bucket-store.index-cache.")
	cfg.ChunksCache.RegisterFlagsWithPrefix(f, "blocks-storage.bucket-store.chunks-cache.", logger)
	cfg.MetadataCache.RegisterFlagsWithPrefix(f, "blocks-storage.bucket-store.metadata-cache.")
	cfg.BucketIndex.RegisterFlagsWithPrefix(f, "blocks-storage.bucket-store.bucket-index.")
	cfg.IndexHeader.RegisterFlagsWithPrefix(f, "blocks-storage.bucket-store.index-header.")

	f.StringVar(&cfg.SyncDir, "blocks-storage.bucket-store.sync-dir", "./tsdb-sync/", "Directory to store synchronized TSDB index headers. This directory is not required to be persisted between restarts, but it's highly recommended in order to improve the store-gateway startup time.")
	f.DurationVar(&cfg.SyncInterval, "blocks-storage.bucket-store.sync-interval", 15*time.Minute, "How frequently to scan the bucket, or to refresh the bucket index (if enabled), in order to look for changes (new blocks shipped by ingesters and blocks deleted by retention or compaction).")
	f.Uint64Var(&cfg.MaxChunkPoolBytes, "blocks-storage.bucket-store.max-chunk-pool-bytes", uint64(2*units.Gibibyte), "Max size - in bytes - of a chunks pool, used to reduce memory allocations. The pool is shared across all tenants. 0 to disable the limit.")
	f.IntVar(&cfg.ChunkPoolMinBucketSizeBytes, "blocks-storage.bucket-store.chunk-pool-min-bucket-size-bytes", ChunkPoolDefaultMinBucketSize, "Size - in bytes - of the smallest chunks pool bucket.")
	f.IntVar(&cfg.ChunkPoolMaxBucketSizeBytes, "blocks-storage.bucket-store.chunk-pool-max-bucket-size-bytes", ChunkPoolDefaultMaxBucketSize, "Size - in bytes - of the largest chunks pool bucket.")
	f.Uint64Var(&cfg.SeriesHashCacheMaxBytes, "blocks-storage.bucket-store.series-hash-cache-max-size-bytes", uint64(1*units.Gibibyte), "Max size - in bytes - of the in-memory series hash cache. The cache is shared across all tenants and it's used only when query sharding is enabled.")
	f.IntVar(&cfg.MaxConcurrent, "blocks-storage.bucket-store.max-concurrent", 100, "Max number of concurrent queries to execute against the long-term storage. The limit is shared across all tenants.")
	f.IntVar(&cfg.TenantSyncConcurrency, "blocks-storage.bucket-store.tenant-sync-concurrency", 10, "Maximum number of concurrent tenants synching blocks.")
	f.IntVar(&cfg.BlockSyncConcurrency, "blocks-storage.bucket-store.block-sync-concurrency", 20, "Maximum number of concurrent blocks synching per tenant.")
	f.IntVar(&cfg.MetaSyncConcurrency, "blocks-storage.bucket-store.meta-sync-concurrency", 20, "Number of Go routines to use when syncing block meta files from object storage per tenant.")
	f.DurationVar(&cfg.DeprecatedConsistencyDelay, consistencyDelayFlag, 0, "Minimum age of a block before it's being read. Set it to safe value (e.g 30m) if your object storage is eventually consistent. GCS and S3 are (roughly) strongly consistent.")
	f.DurationVar(&cfg.IgnoreDeletionMarksDelay, "blocks-storage.bucket-store.ignore-deletion-marks-delay", time.Hour*1, "Duration after which the blocks marked for deletion will be filtered out while fetching blocks. "+
		"The idea of ignore-deletion-marks-delay is to ignore blocks that are marked for deletion with some delay. This ensures store can still serve blocks that are meant to be deleted but do not have a replacement yet.")
	f.DurationVar(&cfg.IgnoreBlocksWithin, "blocks-storage.bucket-store.ignore-blocks-within", 10*time.Hour, "Blocks with minimum time within this duration are ignored, and not loaded by store-gateway. Useful when used together with -querier.query-store-after to prevent loading young blocks, because there are usually many of them (depending on number of ingesters) and they are not yet compacted. Negative values or 0 disable the filter.")
	f.IntVar(&cfg.PostingOffsetsInMemSampling, "blocks-storage.bucket-store.posting-offsets-in-mem-sampling", DefaultPostingOffsetInMemorySampling, "Controls what is the ratio of postings offsets that the store will hold in memory.")
	f.BoolVar(&cfg.IndexHeaderLazyLoadingEnabled, "blocks-storage.bucket-store.index-header-lazy-loading-enabled", true, "If enabled, store-gateway will lazy load an index-header only once required by a query.")
	f.DurationVar(&cfg.IndexHeaderLazyLoadingIdleTimeout, "blocks-storage.bucket-store.index-header-lazy-loading-idle-timeout", 60*time.Minute, "If index-header lazy loading is enabled and this setting is > 0, the store-gateway will offload unused index-headers after 'idle timeout' inactivity.")
	f.Uint64Var(&cfg.PartitionerMaxGapBytes, "blocks-storage.bucket-store.partitioner-max-gap-bytes", DefaultPartitionerMaxGapSize, "Max size - in bytes - of a gap for which the partitioner aggregates together two bucket GET object requests.")
	f.IntVar(&cfg.StreamingBatchSize, "blocks-storage.bucket-store.batch-series-size", 5000, "This option controls how many series to fetch per batch. The batch size must be greater than 0.")
}

// Validate the config.
func (cfg *BucketStoreConfig) Validate(logger log.Logger) error {
	if cfg.StreamingBatchSize <= 0 {
		return errInvalidStreamingBatchSize
	}
	if err := cfg.IndexCache.Validate(); err != nil {
		return errors.Wrap(err, "index-cache configuration")
	}
	if err := cfg.ChunksCache.Validate(); err != nil {
		return errors.Wrap(err, "chunks-cache configuration")
	}
	if err := cfg.MetadataCache.Validate(); err != nil {
		return errors.Wrap(err, "metadata-cache configuration")
	}
	if cfg.DeprecatedConsistencyDelay > 0 {
		util.WarnDeprecatedConfig(consistencyDelayFlag, logger)
	}
	return nil
}

type BucketIndexConfig struct {
	Enabled               bool          `yaml:"enabled"`
	UpdateOnErrorInterval time.Duration `yaml:"update_on_error_interval" category:"advanced"`
	IdleTimeout           time.Duration `yaml:"idle_timeout" category:"advanced"`
	MaxStalePeriod        time.Duration `yaml:"max_stale_period" category:"advanced"`
}

func (cfg *BucketIndexConfig) RegisterFlagsWithPrefix(f *flag.FlagSet, prefix string) {
	f.BoolVar(&cfg.Enabled, prefix+"enabled", true, "If enabled, queriers and store-gateways discover blocks by reading a bucket index (created and updated by the compactor) instead of periodically scanning the bucket.")
	f.DurationVar(&cfg.UpdateOnErrorInterval, prefix+"update-on-error-interval", time.Minute, "How frequently a bucket index, which previously failed to load, should be tried to load again. This option is used only by querier.")
	f.DurationVar(&cfg.IdleTimeout, prefix+"idle-timeout", time.Hour, "How long a unused bucket index should be cached. Once this timeout expires, the unused bucket index is removed from the in-memory cache. This option is used only by querier.")
	f.DurationVar(&cfg.MaxStalePeriod, prefix+"max-stale-period", time.Hour, "The maximum allowed age of a bucket index (last updated) before queries start failing because the bucket index is too old. The bucket index is periodically updated by the compactor, and this check is enforced in the querier (at query time).")
}
