package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStoreConversationFlow(t *testing.T) {
	databaseURL := os.Getenv("CHAT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("CHAT_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := OpenStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.CleanupExpired(ctx); err != nil {
		t.Fatal(err)
	}
	testID := uuid.NewString()

	creatorEmail := "creator-" + testID + "@example.com"
	creator, err := store.UpsertOIDCUser(ctx, "https://issuer.example", OIDCClaims{Subject: "creator-" + testID, Name: "Creator", Email: creatorEmail, EmailVerified: true})
	if err != nil {
		t.Fatal(err)
	}
	memberEmail := "member-" + testID + "@example.com"
	member, err := store.UpsertOIDCUser(ctx, "https://issuer.example", OIDCClaims{Subject: "member-" + testID, Name: "Member", Email: memberEmail, EmailVerified: true})
	if err != nil {
		t.Fatal(err)
	}
	conversation, created, err := store.CreateConversation(ctx, creator.ID, "Test conversation")
	if err != nil {
		t.Fatal(err)
	}
	if created.Seq != 1 || created.CreatedAt.IsZero() {
		t.Fatalf("invalid creation event: %#v", created)
	}

	lookup, err := store.FindUserByEmail(ctx, strings.ToUpper(memberEmail))
	if err != nil {
		t.Fatal(err)
	}
	if lookup.UserID != member.ID || lookup.Email != memberEmail {
		t.Fatalf("unexpected email lookup: %#v", lookup)
	}
	added, joined, err := store.AddMemberByEmail(ctx, creator.ID, conversation.ID, strings.ToUpper(memberEmail), 1000)
	if err != nil || added.UserID != member.ID || joined.Seq != 2 {
		t.Fatalf("unexpected add result: %#v %#v %v", added, joined, err)
	}
	if _, _, err := store.AddMemberByEmail(ctx, creator.ID, conversation.ID, memberEmail, 1000); !errors.Is(err, ErrAlreadyMember) {
		t.Fatalf("expected duplicate member add conflict, got %v", err)
	}

	clientMessageID := uuid.New()
	message, err := store.AppendMessage(ctx, creator.ID, conversation.ID, clientMessageID, "hello")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := store.AppendMessage(ctx, creator.ID, conversation.ID, clientMessageID, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.ID != message.ID || duplicate.Seq != message.Seq {
		t.Fatal("idempotent retry returned a different event")
	}
	events, err := store.ListEvents(ctx, member.ID, conversation.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != "member.joined" || events[1].Type != "message.created" {
		t.Fatalf("unexpected member-visible events: %#v", events)
	}
	if err := store.UpdateRead(ctx, member.ID, conversation.ID, message.Seq); err != nil {
		t.Fatal(err)
	}
	renamed, err := store.RenameConversation(ctx, creator.ID, conversation.ID, "Renamed conversation")
	if err != nil || renamed.Type != "conversation.renamed" {
		t.Fatalf("rename failed: %#v %v", renamed, err)
	}
	members, err := store.ListMembers(ctx, member.ID, conversation.ID)
	if err != nil || len(members) != 2 {
		t.Fatalf("unexpected member list: %#v %v", members, err)
	}
	left, newOwner, err := store.LeaveConversation(ctx, creator.ID, conversation.ID)
	if err != nil || left.Type != "member.left" || newOwner == nil || *newOwner != member.ID {
		t.Fatalf("owner transfer on leave failed: %#v %v %v", left, newOwner, err)
	}
	third, err := store.UpsertOIDCUser(ctx, "https://issuer.example", OIDCClaims{Subject: "third-" + testID, Name: "Third", Email: "third-" + testID + "@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	secondInvite := uuid.NewString() + uuid.NewString()
	if err := store.CreateInvite(ctx, member.ID, conversation.ID, secondInvite, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AcceptInvite(ctx, third.ID, secondInvite, 1000); err != nil {
		t.Fatal(err)
	}
	removed, err := store.RemoveMember(ctx, member.ID, conversation.ID, third.ID)
	if err != nil || removed.Type != "member.removed" {
		t.Fatalf("remove failed: %#v %v", removed, err)
	}
	if _, err := store.ListEvents(ctx, third.ID, conversation.ID, 0, 100); !errors.Is(err, ErrForbidden) {
		t.Fatalf("removed member retained access: %v", err)
	}
}
