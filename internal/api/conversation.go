// Copyright © 2023 OpenIM. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package api

import (
	"github.com/KyleYe/open-im-protocol/conversation"
	"github.com/KyleYe/open-im-server/v3/pkg/rpcclient"
	"github.com/KyleYe/open-im-tools/a2r"
	"github.com/gin-gonic/gin"
)

type ConversationApi rpcclient.Conversation

func NewConversationApi(client rpcclient.Conversation) ConversationApi {
	return ConversationApi(client)
}

func (o *ConversationApi) GetAllConversations(c *gin.Context) {
	a2r.Call(conversation.ConversationClient.GetAllConversations, o.Client, c)
}

func (o *ConversationApi) GetSortedConversationList(c *gin.Context) {
	a2r.Call(conversation.ConversationClient.GetSortedConversationList, o.Client, c)
}

func (o *ConversationApi) GetConversation(c *gin.Context) {
	a2r.Call(conversation.ConversationClient.GetConversation, o.Client, c)
}

func (o *ConversationApi) GetConversations(c *gin.Context) {
	a2r.Call(conversation.ConversationClient.GetConversations, o.Client, c)
}

func (o *ConversationApi) SetConversations(c *gin.Context) {
	a2r.Call(conversation.ConversationClient.SetConversations, o.Client, c)
}

func (o *ConversationApi) GetConversationOfflinePushUserIDs(c *gin.Context) {
	a2r.Call(conversation.ConversationClient.GetConversationOfflinePushUserIDs, o.Client, c)
}

func (o *ConversationApi) GetFullOwnerConversationIDs(c *gin.Context) {
	a2r.Call(conversation.ConversationClient.GetFullOwnerConversationIDs, o.Client, c)
}

func (o *ConversationApi) GetIncrementalConversation(c *gin.Context) {
	a2r.Call(conversation.ConversationClient.GetIncrementalConversation, o.Client, c)
}

func (o *ConversationApi) GetOwnerConversation(c *gin.Context) {
	a2r.Call(conversation.ConversationClient.GetOwnerConversation, o.Client, c)
}
