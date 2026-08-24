package inspection

// SupportedMetrics lists the normalized environmental measurements accepted by the API.
func SupportedMetrics() []string {
	return []string{"temperature", "humidity", "illuminance"}
}
