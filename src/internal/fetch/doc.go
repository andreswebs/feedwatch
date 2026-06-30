// Package fetch performs HTTP retrieval of feeds: conditional GET, the SSRF
// guard, retry with backoff, and charset handling. It defines the Fetcher
// interface consumed by poll.
package fetch
