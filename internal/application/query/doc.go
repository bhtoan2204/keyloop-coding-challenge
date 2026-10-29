// Package query contains the read-side application services. They read the
// PostgreSQL read model only (never the MySQL event store) and are independent of the
// command side. The read model is eventually consistent with the event store.
package query
