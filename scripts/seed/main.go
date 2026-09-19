package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/infrastructure/config"

	_ "AtoiTalkAPI/ent/runtime"
	_ "github.com/lib/pq"
)

func main() {
	cfg := config.LoadAppConfig()
	client := config.InitEnt(cfg)
	defer func() {
		_ = client.Close()
	}()

	ctx := context.Background()

	passwordHash, err := helper.HashPassword("Password123!")
	if err != nil {
		log.Fatalf("failed hashing password: %v", err)
	}

	alice, err := getOrCreateUser(ctx, client, "alice@atoitalk.local", "alice", "Alice Wonderland", "Hello from Alice", passwordHash, user.RoleUser)
	if err != nil {
		log.Fatalf("failed seeding alice: %v", err)
	}

	bob, err := getOrCreateUser(ctx, client, "bob@atoitalk.local", "bob", "Bob Builder", "Can we build it? Yes we can!", passwordHash, user.RoleUser)
	if err != nil {
		log.Fatalf("failed seeding bob: %v", err)
	}

	charlie, err := getOrCreateUser(ctx, client, "charlie@atoitalk.local", "charlie", "Charlie Chaplin", "Silent comedian", passwordHash, user.RoleUser)
	if err != nil {
		log.Fatalf("failed seeding charlie: %v", err)
	}

	admin, err := getOrCreateUser(ctx, client, "admin@atoitalk.local", "admin", "System Administrator", "AtoiTalk Platform Admin", passwordHash, user.RoleAdmin)
	if err != nil {
		log.Fatalf("failed seeding admin: %v", err)
	}

	existingPrivate, _ := client.PrivateChat.Query().First(ctx)

	var privateChatID string
	if existingPrivate != nil {
		privateChatID = existingPrivate.ChatID.String()
	} else {
		privateChatRec, err := client.Chat.Create().
			SetType(chat.TypePrivate).
			Save(ctx)
		if err != nil {
			log.Fatalf("failed creating private chat: %v", err)
		}
		privateChatID = privateChatRec.ID.String()

		_, err = client.PrivateChat.Create().
			SetChatID(privateChatRec.ID).
			SetUser1ID(alice.ID).
			SetUser2ID(bob.ID).
			Save(ctx)
		if err != nil {
			log.Fatalf("failed creating private chat link: %v", err)
		}

		msg1, err := client.Message.Create().
			SetChatID(privateChatRec.ID).
			SetSenderID(alice.ID).
			SetType(message.TypeRegular).
			SetContent("Hi Bob, welcome to AtoiTalk!").
			Save(ctx)
		if err != nil {
			log.Fatalf("failed creating msg1: %v", err)
		}

		msg2, err := client.Message.Create().
			SetChatID(privateChatRec.ID).
			SetSenderID(bob.ID).
			SetReplyToID(msg1.ID).
			SetType(message.TypeRegular).
			SetContent("Thanks Alice! Looks fast and clean.").
			Save(ctx)
		if err != nil {
			log.Fatalf("failed creating msg2: %v", err)
		}

		now := time.Now().UTC()
		_, _ = client.Chat.UpdateOneID(privateChatRec.ID).
			SetLastMessageID(msg2.ID).
			SetLastMessageAt(now).
			Save(ctx)
	}

	existingGroup, _ := client.GroupChat.Query().First(ctx)

	var groupChatID, groupName, groupInviteCode string
	if existingGroup != nil {
		groupChatID = existingGroup.ChatID.String()
		groupName = existingGroup.Name
		groupInviteCode = existingGroup.InviteCode
	} else {
		groupChatRec, err := client.Chat.Create().
			SetType(chat.TypeGroup).
			Save(ctx)
		if err != nil {
			log.Fatalf("failed creating group chat: %v", err)
		}
		groupChatID = groupChatRec.ID.String()

		desc := "Official community group for Golang and high-performance real-time engineering."
		groupInfo, err := client.GroupChat.Create().
			SetChatID(groupChatRec.ID).
			SetCreatedBy(alice.ID).
			SetName("Golang Enthusiasts").
			SetDescription(desc).
			SetIsPublic(true).
			SetInviteCode("GOLANG2026").
			Save(ctx)
		if err != nil {
			log.Fatalf("failed creating group chat info: %v", err)
		}
		groupName = groupInfo.Name
		groupInviteCode = groupInfo.InviteCode

		_, _ = client.GroupMember.Create().
			SetGroupChatID(groupInfo.ID).
			SetUserID(alice.ID).
			SetRole(groupmember.RoleOwner).
			Save(ctx)

		_, _ = client.GroupMember.Create().
			SetGroupChatID(groupInfo.ID).
			SetUserID(bob.ID).
			SetRole(groupmember.RoleAdmin).
			Save(ctx)

		_, _ = client.GroupMember.Create().
			SetGroupChatID(groupInfo.ID).
			SetUserID(charlie.ID).
			SetRole(groupmember.RoleMember).
			Save(ctx)

		groupMsg, err := client.Message.Create().
			SetChatID(groupChatRec.ID).
			SetSenderID(alice.ID).
			SetType(message.TypeRegular).
			SetContent("Welcome everyone to Golang Enthusiasts!").
			Save(ctx)
		if err != nil {
			log.Fatalf("failed creating group message: %v", err)
		}

		now := time.Now().UTC()
		_, _ = client.Chat.UpdateOneID(groupChatRec.ID).
			SetLastMessageID(groupMsg.ID).
			SetLastMessageAt(now).
			Save(ctx)
	}

	for i := 1; i <= 50; i++ {
		email := fmt.Sprintf("loaduser%d@atoitalk.local", i)
		username := fmt.Sprintf("loaduser%d", i)
		fullName := fmt.Sprintf("Load User %d", i)
		_, err := getOrCreateUser(ctx, client, email, username, fullName, "Load testing user account", passwordHash, user.RoleUser)
		if err != nil {
			log.Fatalf("failed seeding load user %d: %v", i, err)
		}
	}

	fmt.Printf("Seed data created successfully!\n")
	fmt.Printf("Users:\n")
	fmt.Printf(" - Alice (user)    : %s (password: Password123!)\n", *alice.Email)
	fmt.Printf(" - Bob (user)      : %s (password: Password123!)\n", *bob.Email)
	fmt.Printf(" - Charlie (user)  : %s (password: Password123!)\n", *charlie.Email)
	fmt.Printf(" - Admin (admin)   : %s (password: Password123!)\n", *admin.Email)
	fmt.Printf(" - Load Users      : loaduser1@atoitalk.local - loaduser50@atoitalk.local (password: Password123!)\n")
	fmt.Printf("Chats:\n")
	fmt.Printf(" - Private Chat ID : %s\n", privateChatID)
	fmt.Printf(" - Group Chat ID   : %s (%s, invite: %s)\n", groupChatID, groupName, groupInviteCode)

	_ = os.Stdout.Sync()
}

func getOrCreateUser(ctx context.Context, client *ent.Client, email, username, fullName, bio, passwordHash string, role user.Role) (*ent.User, error) {
	u, err := client.User.Query().Where(user.Email(email)).Only(ctx)
	if err == nil {
		return u, nil
	}
	if !ent.IsNotFound(err) {
		return nil, err
	}

	return client.User.Create().
		SetEmail(email).
		SetUsername(username).
		SetPasswordHash(passwordHash).
		SetFullName(fullName).
		SetBio(bio).
		SetRole(role).
		Save(ctx)
}
