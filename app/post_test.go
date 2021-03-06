// Copyright (c) 2017-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-server/model"
)

func TestUpdatePostEditAt(t *testing.T) {
	th := Setup().InitBasic()
	defer th.TearDown()

	post := &model.Post{}
	*post = *th.BasicPost

	post.IsPinned = true
	if saved, err := th.App.UpdatePost(post, true); err != nil {
		t.Fatal(err)
	} else if saved.EditAt != post.EditAt {
		t.Fatal("shouldn't have updated post.EditAt when pinning post")

		*post = *saved
	}

	time.Sleep(time.Millisecond * 100)

	post.Message = model.NewId()
	if saved, err := th.App.UpdatePost(post, true); err != nil {
		t.Fatal(err)
	} else if saved.EditAt == post.EditAt {
		t.Fatal("should have updated post.EditAt when updating post message")
	}
}

func TestPostReplyToPostWhereRootPosterLeftChannel(t *testing.T) {
	// This test ensures that when replying to a root post made by a user who has since left the channel, the reply
	// post completes successfully. This is a regression test for PLT-6523.
	th := Setup().InitBasic()
	defer th.TearDown()

	channel := th.BasicChannel
	userInChannel := th.BasicUser2
	userNotInChannel := th.BasicUser
	rootPost := th.BasicPost

	if _, err := th.App.AddUserToChannel(userInChannel, channel); err != nil {
		t.Fatal(err)
	}

	if err := th.App.RemoveUserFromChannel(userNotInChannel.Id, "", channel); err != nil {
		t.Fatal(err)
	}

	replyPost := model.Post{
		Message:       "asd",
		ChannelId:     channel.Id,
		RootId:        rootPost.Id,
		ParentId:      rootPost.Id,
		PendingPostId: model.NewId() + ":" + fmt.Sprint(model.GetMillis()),
		UserId:        userInChannel.Id,
		CreateAt:      0,
	}

	if _, err := th.App.CreatePostAsUser(&replyPost); err != nil {
		t.Fatal(err)
	}
}

func TestPostAction(t *testing.T) {
	th := Setup().InitBasic()
	defer th.TearDown()

	th.App.UpdateConfig(func(cfg *model.Config) {
		*cfg.ServiceSettings.AllowedUntrustedInternalConnections = "localhost 127.0.0.1"
	})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request model.PostActionIntegrationRequest
		err := json.NewDecoder(r.Body).Decode(&request)
		assert.NoError(t, err)
		assert.Equal(t, request.UserId, th.BasicUser.Id)
		assert.Equal(t, "foo", request.Context["s"])
		assert.EqualValues(t, 3, request.Context["n"])
		fmt.Fprintf(w, `{"update": {"message": "updated"}, "ephemeral_text": "foo"}`)
	}))
	defer ts.Close()

	interactivePost := model.Post{
		Message:       "Interactive post",
		ChannelId:     th.BasicChannel.Id,
		PendingPostId: model.NewId() + ":" + fmt.Sprint(model.GetMillis()),
		UserId:        th.BasicUser.Id,
		Props: model.StringInterface{
			"attachments": []*model.SlackAttachment{
				{
					Text: "hello",
					Actions: []*model.PostAction{
						{
							Integration: &model.PostActionIntegration{
								Context: model.StringInterface{
									"s": "foo",
									"n": 3,
								},
								URL: ts.URL,
							},
							Name: "action",
						},
					},
				},
			},
		},
	}

	post, err := th.App.CreatePostAsUser(&interactivePost)
	require.Nil(t, err)

	attachments, ok := post.Props["attachments"].([]*model.SlackAttachment)
	require.True(t, ok)

	require.NotEmpty(t, attachments[0].Actions)
	require.NotEmpty(t, attachments[0].Actions[0].Id)

	err = th.App.DoPostAction(post.Id, "notavalidid", th.BasicUser.Id)
	require.NotNil(t, err)
	assert.Equal(t, http.StatusNotFound, err.StatusCode)

	err = th.App.DoPostAction(post.Id, attachments[0].Actions[0].Id, th.BasicUser.Id)
	require.Nil(t, err)
}

func TestPostChannelMentions(t *testing.T) {
	th := Setup().InitBasic()
	defer th.TearDown()

	channel := th.BasicChannel
	user := th.BasicUser

	channelToMention, err := th.App.CreateChannel(&model.Channel{
		DisplayName: "Mention Test",
		Name:        "mention-test",
		Type:        model.CHANNEL_OPEN,
		TeamId:      th.BasicTeam.Id,
	}, false)
	if err != nil {
		t.Fatal(err.Error())
	}
	defer th.App.PermanentDeleteChannel(channelToMention)

	_, err = th.App.AddUserToChannel(user, channel)
	require.Nil(t, err)

	post := &model.Post{
		Message:       fmt.Sprintf("hello, ~%v!", channelToMention.Name),
		ChannelId:     channel.Id,
		PendingPostId: model.NewId() + ":" + fmt.Sprint(model.GetMillis()),
		UserId:        user.Id,
		CreateAt:      0,
	}

	result, err := th.App.CreatePostAsUser(post)
	require.Nil(t, err)
	assert.Equal(t, map[string]interface{}{
		"mention-test": map[string]interface{}{
			"display_name": "Mention Test",
		},
	}, result.Props["channel_mentions"])

	post.Message = fmt.Sprintf("goodbye, ~%v!", channelToMention.Name)
	result, err = th.App.UpdatePost(post, false)
	require.Nil(t, err)
	assert.Equal(t, map[string]interface{}{
		"mention-test": map[string]interface{}{
			"display_name": "Mention Test",
		},
	}, result.Props["channel_mentions"])
}

func TestImageProxy(t *testing.T) {
	th := Setup().InitBasic()
	defer th.TearDown()

	for name, tc := range map[string]struct {
		ProxyType       string
		ProxyURL        string
		ProxyOptions    string
		ImageURL        string
		ProxiedImageURL string
	}{
		"atmos/camo": {
			ProxyType:       "atmos/camo",
			ProxyURL:        "https://127.0.0.1",
			ProxyOptions:    "foo",
			ImageURL:        "http://mydomain.com/myimage",
			ProxiedImageURL: "https://127.0.0.1/f8dace906d23689e8d5b12c3cefbedbf7b9b72f5/687474703a2f2f6d79646f6d61696e2e636f6d2f6d79696d616765",
		},
		"willnorris/imageproxy": {
			ProxyType:       "willnorris/imageproxy",
			ProxyURL:        "https://127.0.0.1",
			ProxyOptions:    "x1000",
			ImageURL:        "http://mydomain.com/myimage",
			ProxiedImageURL: "https://127.0.0.1/x1000/http://mydomain.com/myimage",
		},
		"willnorris/imageproxy_WithSigning": {
			ProxyType:       "willnorris/imageproxy",
			ProxyURL:        "https://127.0.0.1",
			ProxyOptions:    "x1000|foo",
			ImageURL:        "http://mydomain.com/myimage",
			ProxiedImageURL: "https://127.0.0.1/x1000,sbhHVoG5d60UvnNtGh6Iy6x4PaMmnsh8JfZ7JfErKjGU=/http://mydomain.com/myimage",
		},
	} {
		t.Run(name, func(t *testing.T) {
			th.App.UpdateConfig(func(cfg *model.Config) {
				cfg.ServiceSettings.ImageProxyType = model.NewString(tc.ProxyType)
				cfg.ServiceSettings.ImageProxyOptions = model.NewString(tc.ProxyOptions)
				cfg.ServiceSettings.ImageProxyURL = model.NewString(tc.ProxyURL)
			})

			post := &model.Post{
				Id:      model.NewId(),
				Message: "![foo](" + tc.ImageURL + ")",
			}

			list := model.NewPostList()
			list.Posts[post.Id] = post

			assert.Equal(t, "![foo]("+tc.ProxiedImageURL+")", th.App.PostListWithProxyAddedToImageURLs(list).Posts[post.Id].Message)
			assert.Equal(t, "![foo]("+tc.ProxiedImageURL+")", th.App.PostWithProxyAddedToImageURLs(post).Message)

			assert.Equal(t, "![foo]("+tc.ImageURL+")", th.App.PostWithProxyRemovedFromImageURLs(post).Message)
			post.Message = "![foo](" + tc.ProxiedImageURL + ")"
			assert.Equal(t, "![foo]("+tc.ImageURL+")", th.App.PostWithProxyRemovedFromImageURLs(post).Message)
		})
	}
}

var imageProxyBenchmarkSink *model.Post

func BenchmarkPostWithProxyRemovedFromImageURLs(b *testing.B) {
	th := Setup().InitBasic()
	defer th.TearDown()

	th.App.UpdateConfig(func(cfg *model.Config) {
		cfg.ServiceSettings.ImageProxyType = model.NewString("willnorris/imageproxy")
		cfg.ServiceSettings.ImageProxyOptions = model.NewString("x1000|foo")
		cfg.ServiceSettings.ImageProxyURL = model.NewString("https://127.0.0.1")
	})

	post := &model.Post{
		Message: "![foo](http://mydomain.com/myimage)",
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		imageProxyBenchmarkSink = th.App.PostWithProxyAddedToImageURLs(post)
	}
}
