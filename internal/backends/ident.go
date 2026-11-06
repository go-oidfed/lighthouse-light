package backends

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
)

// hashID derives a stable numeric ID from the identifying values of a row
// (entity_id, trust_mark_type, etc.). IDs are content-addressed: they never
// appear in the data files, are stable across restarts, and keep persisted
// cross-references (e.g. trust_mark_subject_id in issued instances) valid
// after re-boots. The low 63 bits of FNV-1a-64 are used (positive int64,
// matching the uint fields on the model structs).
func hashID(parts ...string) uint {
	h := fnv.New64a()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(p))
	}
	return uint(h.Sum64() & (1<<63 - 1))
}

// checkNoIDCollision returns an error when two distinct identities produce the
// same hash ID — astronomically unlikely at POC scale, but a silent merge
// would corrupt lookups, so fail fast.
func checkNoIDCollision(store string, seen map[uint]string, id uint, ident string) error {
	if other, exists := seen[id]; exists {
		return fmt.Errorf(
			"store %s: ID collision between %q and %q (hash %d); rename one of the identifiers",
			store, other, ident, id,
		)
	}
	seen[id] = ident
	return nil
}

// rejectLegacyIDs errors when a data file still uses the pre-v0.2 numeric-ID
// format (next_* counters at top level and an "id" member in every row).
// lighthouse-light now derives IDs from content (see hashID); legacy files
// must be re-provisioned. containerKeys lists the JSON array member names
// whose elements may carry an "id" member (e.g. "subordinates", "hints").
func rejectLegacyIDs(file, store string, containerKeys ...string) error {
	content, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("store %s: failed to read %s: %w", store, file, err)
	}
	if len(content) == 0 {
		return nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(content, &top); err != nil {
		// Unparseable files are reported by the store's own load error.
		return nil
	}
	if _, ok := top["next_id"]; ok {
		return legacyErr(file)
	}
	for _, key := range containerKeys {
		raw, ok := top[key]
		if !ok {
			continue
		}
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &rows); err != nil {
			continue
		}
		for _, row := range rows {
			if _, ok := row["id"]; ok {
				return legacyErr(file)
			}
		}
	}
	return nil
}

func legacyErr(file string) error {
	return fmt.Errorf(
		"store %s: file uses the legacy numeric-ID format (removed in lighthouse-light v0.2); IDs are now derived from entity IDs — re-provision the file (see docs/data-directory.md)",
		file,
	)
}

// rejectWrappedFormat fails boot when a data file uses the pre-v0.3
// top-level-object wrapper (e.g. {"anchors":[ ... ]}); files are now bare
// arrays/objects. Missing/empty/non-object files pass.
func rejectWrappedFormat(file, store string, containerKeys ...string) error {
	content, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("store %s: failed to read %s: %w", store, file, err)
	}
	if len(content) == 0 {
		return nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(content, &top); err != nil {
		// Bare array/other top-level: not a wrapper; the store's own load
		// reports unparseable content.
		return nil
	}
	for _, key := range containerKeys {
		if _, ok := top[key]; ok {
			return wrappedErr(store, file, key)
		}
	}
	return nil
}

func wrappedErr(store, file, key string) error {
	return fmt.Errorf(
		"store %s: %s: file uses the pre-v0.3 format (a top-level \"%s\" wrapper); data files are now bare arrays/objects — re-provision (see docs/data-directory.md)",
		store, file, key,
	)
}
