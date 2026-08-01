package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
)

const defaultAvatarSize = 96

// gravatarURL follows Gravatar's current email normalization and SHA-256 format.
// The identicon default also gives accounts without an email a stable placeholder.
func gravatarURL(email string, size int) string {
	normalized := strings.ToLower(strings.TrimSpace(email))
	digest := sha256.Sum256([]byte(normalized))
	query := url.Values{
		"d": {"identicon"},
		"r": {"g"},
		"s": {strconv.Itoa(size)},
	}
	return "https://gravatar.com/avatar/" + hex.EncodeToString(digest[:]) + "?" + query.Encode()
}

func addUserAvatar(user User) User {
	user.AvatarURL = gravatarURL(user.Email, defaultAvatarSize)
	return user
}

func addMemberAvatar(member Member) Member {
	member.AvatarURL = gravatarURL(member.Email, defaultAvatarSize)
	return member
}

func addLookupAvatar(user UserLookup) UserLookup {
	user.AvatarURL = gravatarURL(user.Email, defaultAvatarSize)
	return user
}
