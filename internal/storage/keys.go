package storage

import "path"

// The bucket name is not part of the key.
func SourceKey(mediaID, fileName string) string {
	return path.Join(mediaID, "original", fileName)
}

func HLSPrefix(mediaID string) string {
	return path.Join(mediaID, "hls")
}

func HLSKey(mediaID, filePath string) string {
	return path.Join(HLSPrefix(mediaID), filePath)
}
