package storage

const (
	snapshotFilename = "snapshot.json"
	eventsFilename   = "events.log"
)

// SnapshotFilename and EventsFilename keep persistence names discoverable to diagnostics.
func SnapshotFilename() string { return snapshotFilename }
func EventsFilename() string   { return eventsFilename }
