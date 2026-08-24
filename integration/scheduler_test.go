//go:build integration

package integration

import (
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/media"
	"AtoiTalkAPI/ent/privatechat"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/scheduler/job"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSchedulerJobs(t *testing.T) {
	clearDatabase(context.Background())

	originalRetention := testConfig.SoftDeleteRetentionDays
	testConfig.SoftDeleteRetentionDays = 0
	defer func() {
		testConfig.SoftDeleteRetentionDays = originalRetention
	}()

	t.Run("Entity Cleanup - Hard Delete User and Unlink Private Chat", func(t *testing.T) {
		ctx := context.Background()
		u1 := createTestUser(t, "user_sched_1")
		u2 := createTestUser(t, "user_sched_2")

		chatEntity := testClient.Chat.Create().SetType("private").SaveX(ctx)
		pc := testClient.PrivateChat.Create().
			SetChat(chatEntity).
			SetUser1(u1).
			SetUser2(u2).
			SaveX(ctx)

		testClient.User.UpdateOne(u1).SetDeletedAt(time.Now().Add(-24 * time.Hour)).ExecX(ctx)

		err := job.RunEntityCleanup(ctx, testClient, testConfig)
		assert.NoError(t, err)

		exists, _ := testClient.User.Query().Where(user.ID(u1.ID)).Exist(ctx)
		assert.False(t, exists)

		exists, _ = testClient.User.Query().Where(user.ID(u2.ID)).Exist(ctx)
		assert.True(t, exists)

		pcAfter, err := testClient.PrivateChat.Query().Where(privatechat.ID(pc.ID)).Only(ctx)
		assert.NoError(t, err)
		assert.Nil(t, pcAfter.User1ID)
		assert.NotNil(t, pcAfter.User2ID)
		assert.Equal(t, u2.ID, *pcAfter.User2ID)
	})

	t.Run("Private Chat Cleanup - Garbage Collect Abandoned Chats", func(t *testing.T) {
		ctx := context.Background()
		u3 := createTestUser(t, "user_sched_3")
		u4 := createTestUser(t, "user_sched_4")

		chatEntity := testClient.Chat.Create().SetType("private").SaveX(ctx)
		pc := testClient.PrivateChat.Create().
			SetChat(chatEntity).
			SetUser1(u3).
			SetUser2(u4).
			SaveX(ctx)

		testClient.User.UpdateOne(u3).SetDeletedAt(time.Now().Add(-24 * time.Hour)).ExecX(ctx)
		testClient.User.UpdateOne(u4).SetDeletedAt(time.Now().Add(-24 * time.Hour)).ExecX(ctx)

		err := job.RunEntityCleanup(ctx, testClient, testConfig)
		assert.NoError(t, err)

		pcAfter, _ := testClient.PrivateChat.Query().Where(privatechat.ID(pc.ID)).Only(ctx)
		assert.Nil(t, pcAfter.User1ID)
		assert.Nil(t, pcAfter.User2ID)

		err = job.RunPrivateChatCleanup(ctx, testClient, testConfig)
		assert.NoError(t, err)

		exists, _ := testClient.PrivateChat.Query().Where(privatechat.ID(pc.ID)).Exist(ctx)
		assert.False(t, exists)

		chatExists, _ := testClient.Chat.Query().Where(chat.ID(chatEntity.ID)).Exist(ctx)
		assert.False(t, chatExists)
	})

	t.Run("Media Cleanup - Delete Orphaned Media", func(t *testing.T) {
		originalMediaRetention := testConfig.MediaRetentionDays
		testConfig.MediaRetentionDays = 0
		defer func() {
			testConfig.MediaRetentionDays = originalMediaRetention
		}()

		ctx := context.Background()
		u := createTestUser(t, "media_uploader")

		mediaItem, _ := testClient.Media.Create().
			SetFileName("orphan.jpg").
			SetOriginalName("orphan.jpg").
			SetFileSize(100).
			SetMimeType("image/jpeg").
			SetUploader(u).
			SetCreatedAt(time.Now().Add(-24 * time.Hour)).
			Save(ctx)

		err := job.RunMediaCleanup(ctx, testClient, testStorageAdapter, testConfig)
		assert.NoError(t, err)

		exists, _ := testClient.Media.Query().Where(media.ID(mediaItem.ID)).Exist(ctx)
		assert.False(t, exists)
	})

	t.Run("Media Cleanup - Safety Check Attached Media", func(t *testing.T) {
		originalMediaRetention := testConfig.MediaRetentionDays
		testConfig.MediaRetentionDays = 0
		defer func() {
			testConfig.MediaRetentionDays = originalMediaRetention
		}()

		ctx := context.Background()
		u := createTestUser(t, "media_safe_uploader")
		chatEntity := testClient.Chat.Create().SetType("private").SaveX(ctx)

		mediaAttached, _ := testClient.Media.Create().
			SetFileName("attached.jpg").
			SetOriginalName("attached.jpg").
			SetFileSize(100).
			SetMimeType("image/jpeg").
			SetUploader(u).
			SetCreatedAt(time.Now().Add(-24 * time.Hour)).
			Save(ctx)

		testClient.Message.Create().
			SetChat(chatEntity).
			SetSender(u).
			SetType("regular").
			SetContent("test").
			AddAttachments(mediaAttached).
			SaveX(ctx)

		err := job.RunMediaCleanup(ctx, testClient, testStorageAdapter, testConfig)
		assert.NoError(t, err)

		exists, _ := testClient.Media.Query().Where(media.ID(mediaAttached.ID)).Exist(ctx)
		assert.True(t, exists)
	})

	t.Run("Private Chat Cleanup - Safety Check Active User", func(t *testing.T) {
		ctx := context.Background()
		u5 := createTestUser(t, "user_sched_5")
		u6 := createTestUser(t, "user_sched_6")

		chatEntity := testClient.Chat.Create().SetType("private").SaveX(ctx)
		pc := testClient.PrivateChat.Create().
			SetChat(chatEntity).
			SetUser1(u5).
			SetUser2(u6).
			SaveX(ctx)

		testClient.User.UpdateOne(u5).SetDeletedAt(time.Now().Add(-24 * time.Hour)).ExecX(ctx)

		err := job.RunEntityCleanup(ctx, testClient, testConfig)
		assert.NoError(t, err)

		err = job.RunPrivateChatCleanup(ctx, testClient, testConfig)
		assert.NoError(t, err)

		exists, _ := testClient.PrivateChat.Query().Where(privatechat.ID(pc.ID)).Exist(ctx)
		assert.True(t, exists)
	})
}
