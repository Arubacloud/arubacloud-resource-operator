package util

// CalculateVolumeChanges compares desired volume IDs with current volume IDs
// and returns lists of volumes to attach and detach
func CalculateVolumeChanges(desiredVolumeIDs, currentVolumeIDs []string) (toAttach, toDetach []string) {
	// Create maps for efficient lookup
	desiredMap := make(map[string]bool)
	currentMap := make(map[string]bool)

	for _, id := range desiredVolumeIDs {
		desiredMap[id] = true
	}

	for _, id := range currentVolumeIDs {
		currentMap[id] = true
	}

	// Find volumes to attach (in desired but not in current)
	for _, id := range desiredVolumeIDs {
		if !currentMap[id] {
			toAttach = append(toAttach, id)
		}
	}

	// Find volumes to detach (in current but not in desired)
	for _, id := range currentVolumeIDs {
		if !desiredMap[id] {
			toDetach = append(toDetach, id)
		}
	}

	return toAttach, toDetach
}
