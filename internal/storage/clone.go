package storage

// SnapshotDigest returns the deterministic digest used by audit verification.
func (s *Store) SnapshotDigest() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Digest(s.data)
}
