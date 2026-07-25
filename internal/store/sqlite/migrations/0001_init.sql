CREATE TABLE feeds (
    url TEXT PRIMARY KEY,
    alias TEXT UNIQUE,
    interval_seconds INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'active',
    etag TEXT NOT NULL DEFAULT '',
    last_modified TEXT NOT NULL DEFAULT '',
    failure_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    last_error_at TEXT,
    last_fetch_at TEXT,
    next_due_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE items (
    feed_url TEXT NOT NULL REFERENCES feeds (url) ON DELETE CASCADE,
    dedup_key TEXT NOT NULL,
    guid TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    link TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    content_html TEXT NOT NULL DEFAULT '',
    content_text TEXT NOT NULL DEFAULT '',
    content_mime_type TEXT NOT NULL DEFAULT '',
    base_url TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    categories TEXT NOT NULL DEFAULT '[]',
    enclosures TEXT NOT NULL DEFAULT '[]',
    published_at TEXT,
    updated_at TEXT,
    fetched_at TEXT NOT NULL,
    seen INTEGER NOT NULL DEFAULT 0,
    tombstoned INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (feed_url, dedup_key)
);

CREATE INDEX idx_items_feed_published ON items (feed_url, published_at);
CREATE INDEX idx_items_fetched ON items (fetched_at);
