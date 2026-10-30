// Package projection declares what the read-model projections need from storage: the
// transaction-bound write port and the projected models it reads and writes. It is
// owned by the projection use case; infrastructure (viewstore) implements it.
package projection
