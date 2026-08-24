# Changelog

## [0.1.1](https://github.com/Hilmi-Raif/Atoi-Talk-API/compare/atoitalk-api-v0.1.0...atoitalk-api-v0.1.1) (2026-08-24)


### Features

* add 1-sided chat deletion and message soft-delete with refined visibility logic ([45259f4](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/45259f4bffb0fe72365ec23a9c1e96e793600cd5))
* add blocked users table and remove pinned chat message ([00db07f](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/00db07f3ecec5d4977ff69b058bb09771d7c0387))
* add capability to exclude group members from user search ([db2eac3](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/db2eac315b1154c2d9ea8cfbe789d746f9328d1b))
* add captcha to media upload endpoint and update tests ([509abbd](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/509abbdfd8f3abfc854d1261c94c36e506b21c30))
* add consistent fields to chat.new event ([28e6113](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/28e6113a0e8af86bc0daed9348251b624f0cffa8))
* add get current user endpoint ([3a377d0](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/3a377d0a90bc1ad998a8292285df8b4f305b222e))
* add Google OAuth login and registration endpoint ([ada29d4](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/ada29d45b13c3dd68cb86a82ac05817597cbace1))
* add group chat creation and multi device sync for realtime event ([9e07126](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/9e0712691972f029774fe9bd298c96c8f707084e))
* add group management features (leave, promote/demote, transfer ownership) and expose my_role in chat list ([8c988e9](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/8c988e9767f5aef7c36942652fe7f6ad59b21c39))
* add member_count to group action responses and ws events ([ad567a3](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/ad567a37a57b94bd52194d9c846aa3057f677195))
* add optional include_chat_id param to user search ([eb5166a](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/eb5166a17fac1944492c3acbf41bccb9b13fa7c3))
* add other_last_read_at to chat response for read status tracking ([9b817ca](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/9b817ca4c1581cde4e0289eff522372d1f06eac4))
* add permanent invite links for public groups and send chat.delete event on kick ([0fd565f](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/0fd565fdbf75afb31816cbfc3a4f5b7ccee6d214))
* add private chat creation with tests and retry mechanism refactor ([1308d3a](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/1308d3a70a8d0eaf50db57d6e8a39e7221d7ff39))
* add profile picture download from Google OAuth and storage with local/s3 support ([955a7d8](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/955a7d81dc2d1ebfa6739abd3bd8160e5f8d0e4e))
* add public groups, invite links with expiration, and group preview functionality ([0f50e0c](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/0f50e0c42202eca9e9dafa6bf0794c5869355089))
* add report logic, admin role workflows, and user ban validation ([be236c4](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/be236c4e04c4cc3c02f8777b34a59a7b81d76c37))
* add scheduler for entity expiration, media cleanup, and abandoned chat removal ([19d8cf7](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/19d8cf71a1d8a8d068bd4732b7364a7e4227d56f))
* add sender role to response ([8e2f0ef](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/8e2f0ef34a27bc1284b81ae8d1938b89bd3953cc))
* add sender_id to reply_to in message response ([1291b36](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/1291b36e714bddc35c8f677d1dccff70b72f19f9))
* add sort_by param to public group search and use prefix-based search ([8240676](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/82406766b8d7199364ffa9c873f897da522662ed))
* add temp codes schema and SMTP configuration ([735b235](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/735b23557cecf750f88ccf51aea187455100cbd0))
* add user online and block status to API responses and update tests ([db68405](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/db68405f707527623560e9fc4abe4a2a3eb11959))
* add usernames, update blocking logic, and expand test scenarios ([b6c1216](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/b6c12168059189f3089f4f32a5513baa3cdeab6e))
* add websocket support with tests and asyncapi docs ([2d32b6a](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/2d32b6a9811ac939b1173f3f357ef5fa882e125c))
* auto reset invite code on private switch and add expiry to chat response ([7fb408a](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/7fb408a6624aff1a750bfaf9d61e110c51eb29fc))
* broadcast new invite code to admin on update & reset ([e9abbda](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/e9abbda08e0559622d307659ccce4edb543e246a))
* complete admin dashboard suite, invite security, and ws broadcast fixes ([677809b](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/677809b0fd231dd43abcc53a57c8b02b416b24dd))
* enforce google oauth state+pkce and harden chat consistency ([2189ab8](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/2189ab891a347b62c1bf867e80ce948e7944538a))
* enhance group update with description, visibility, and avatar deletion ([c07a2de](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/c07a2deffc0b5790cbe5701d9645801f689ca55d))
* enhance privacy with hard block, ninja read, and update tests ([5786294](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/57862943095be393a5e0e4f863c65895c972a2af))
* enhance report resolution with audit trail & fix member count ([219f2f2](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/219f2f2ef9430bc4e77284a3b9f31fd2e1c3760e))
* implement add member logic, system messages, and refactor timezone handling to UTC with security improvements ([07fe9cc](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/07fe9cc626feb1c8bdb0701b2a70f94774caca33))
* implement api rate limiting with redis ([5fae90d](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/5fae90dc2df453f44b17b9a294bf07aa7976131c))
* implement auth middleware ([1690a92](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/1690a928b483898cfec4984a9fd30a9f1ca56229))
* implement bidirectional cursor pagination for messages ([4ea135d](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/4ea135dc3ace02d3e4f73b789fda7c9c32dedf85))
* implement chat history endpoint with cursor pagination ([fe610a1](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/fe610a1f03120ec30021825c5468a3d0413c33eb))
* implement delete report endpoint ([c61ebad](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/c61ebad595239e7449385d08cb0577d664eeac24))
* implement edit group info with system messages and realtime updates ([d59ece3](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/d59ece331e91272a3ab21bb2b51d4756abf0abea))
* implement edit message functionality with attachment support and realtime updates ([e9d90ce](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/e9d90ceb56903b085eba36ac27712b7507b75b06))
* implement kick member functionality with system messages and realtime updates ([7719870](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/7719870b908e409fee240c6775460586da62bf80))
* implement media upload with transaction safety and message creation with lazy attachment linking ([10463af](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/10463af545ffab3d3ad85a80fbc5071ad02e6703))
* implement optimized Redis TTL for user presence ([f487d8d](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/f487d8d6d1e14d43ba1d90cd0a1e7da8261a8106))
* implement OTP sending with rate limiting, captcha verification, and email templates ([709ed39](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/709ed395c3831e63f1a69155052ecd933ef53a1d))
* implement paginated chat search and unread count logic ([5408cd1](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/5408cd1c95cd89b1a5fe76dc092a57db6d6f795e))
* implement reset password and refine OTP handling ([a874dc0](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/a874dc0821d82127954d1ce482e1c15673c741e9))
* implement search group members with pagination ([7f72b74](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/7f72b74769f747ba877bd3d543c7e82e9876a257))
* implement soft delete for group chats ([d1ed435](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/d1ed4358abd7c1224c52d9ba38ef0014b3a49af9))
* implement soft delete for user accounts ([6114ab0](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/6114ab07718b2b6a27b9bfd56949263e2009a9b8))
* implement user login with captcha validation ([cc28075](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/cc28075ee1dac261cef672c2e28c14ab87b85198))
* implement user profile, password, and email update features ([68c9806](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/68c9806a00da5082a425a3cc9063eaf95efcd397))
* implement user registration with OTP verification and captcha ([5183ec3](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/5183ec3969ddaced2809e436d9bd85d7cb6d96b0))
* implement user search with cursor-based pagination and tests ([1c6ba8f](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/1c6ba8f7bbf7933d20cbf83cbc20214c40d4c8eb))
* include invite code in group creation response ([3eb70de](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/3eb70debd9ffe99306b438fc156a66e673056404))
* limiting search query length ([ed49cdd](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/ed49cdd2426820409cfdc09e86e17632bb4a93b0))
* migrate media uploads to presigned direct upload flow ([6f16733](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/6f16733a38e13aa700a0e052d1e464d59170b096))
* migrate otp and online status to redis, implement pub sub for stateless websocket, and optimize chat events handling ([435755e](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/435755e98acb994d37f81144a1c83f6a5cc9923a))
* optimize user search with prefix match, trim input fields, and document snapshot structure ([baaec0a](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/baaec0a19f7f2475e8ccbf20ecbf7c4182b188f3))
* optimize ws payloads and add get chat by id endpoint ([ec18345](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/ec183457973771958b0986c597bf974b0a424604))
* overhaul report evidence, user status visibility, and fix pagination ([cf9aa25](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/cf9aa252d47997c89ffe506b8a7ca8015803a369))
* return system messages in api responses and inject target_name in ws events ([388977b](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/388977ba95554ff41beda69b34024c3b486afb0c))
* setup docker, timezone, and fix report deletion logic ([e36b893](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/e36b893fc3456e4577dcbf35330dd93291a1a077))
* unify response structure, add structured logging, refactor router init with CORS, and update API docs ([ed2cfad](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/ed2cfada3345d1f9ddc2e68e122e84745b54dc4c))
* update verification email template content ([fc855d9](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/fc855d9c0dee85bc5bd5a437bde5d814a1c7a0f9))


### Bug Fixes

* handle deleted accounts correctly in group management and message retrieval workflows ([4e2ded4](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/4e2ded436873aa85af8b45bd5d1310c188ce2a50))
* harden session consistency and error handling ([2b8e7a1](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/2b8e7a1bb8a5ea7614e87ad75414e598d48a9f1e))
* harden session revocation, ws disconnect, and upload/invite edge-case handling ([a027f40](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/a027f4020d20bb4c8f38e6dfb3de9c6495423447))
* harden session revoke precision and realtime/rate-limit reliability ([b290167](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/b290167df44df6862163b07c37326b00e8f53247))
* hide empty chats and deleted members from lists ([0102b1f](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/0102b1f6387b8c2a3f01e54c648e8742a5a44c58))
* inconsistent admin group identifiers by standardizing on chat_id and harden moderation/media/OTP edge-case handling ([cb2d3db](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/cb2d3db0361021e59112e287330eefe76a157989))
* move OTP rate limit check after validation ([e94c417](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/e94c417e2a8ce4d058220461d6712a0b86d7d3f6))
* prevent nil panic and inconsistent chat state in core services ([6594d8e](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/6594d8eab9cf434088468bf393e3b52ada4cf387))
* resolve logic flaws in media upload sequences, transaction handling, and chat query filters ([d17a851](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/d17a85138346a7e4541acb14822717ae55b0c570))
* resolve race condition in registration ([44e1b49](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/44e1b4916e7547a234fad5db57dd34bfa2320d35))
* standardize chat response contracts ([bde888e](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/bde888edf7b69828d906dbd537a33d4056c03e14))
* standardize message response format and handle deleted messages ([d8da607](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/d8da607b2235373f59968a90c6ac445e565e55dd))
* store s3 keys in report evidence ([12bea64](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/12bea64c519130dd26430c4be12d59e4544c0963))
* **websocket:** prevent redis receive hang in race test ([8313515](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/83135152e091db03ee89f25a5f3bcb0901ae78fb))


### Performance Improvements

* add postgres indexes for query hot paths ([f2e9df4](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/f2e9df4813a9ea2d268b483d5868477aa76c44a4))
* **api:** optimize queries and harden test pipeline ([38ccd33](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/38ccd33fb7dc12139d336588115feb2ac7f12249))
* optimize database queries by selecting only necessary fields in services and repositories ([a3884dd](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/a3884dd51eb0a83a150f76e35b380d286df842d1))
* optimize jwt processing by removing double parsing ([062aa86](https://github.com/Hilmi-Raif/Atoi-Talk-API/commit/062aa86f14a05531a3c4eda713bfce7f68363460))

## Changelog

All notable changes to this project will be documented in this file.

The format is based on Keep a Changelog and this project adheres to Semantic Versioning.
