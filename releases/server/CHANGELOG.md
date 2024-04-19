## Version 0.8.4
- Add support for new OpenAI models: `gpt-4-turbo` and `gpt-4-turbo-2024-04-09`
- Make `gpt-4-turbo` model the new default model for the planner, builder, and auto-continue roles -- in testing it seems to be better at reasoning and significantly less lazy than the previous default for these roles, `gpt-4-turbo-preview` -- any plan that has not previously had its model settings modified will now use `gpt-4-turbo` by default (those that have been modified will need to be updated manually) -- remember that you can always use `plandex set-model` to change models for your plans
- Fix for handling files that are loaded into context and later deleted from the file system (https://github.com/plandex-ai/plandex/issues/47)
- Handle file paths with ### prefixes (https://github.com/plandex-ai/plandex/issues/77)
- Fix for occasional race condition during file builds that causes error "Fatal: Unable to write new index file"

## Version 0.8.3
- SMTP_FROM environment variable for setting from address when self-hosting and using SMTP (https://github.com/plandex-ai/plandex/pull/39)
- Add support for OPENAI_ENDPOINT environment variable for custom OpenAI endpoints (https://github.com/plandex-ai/plandex/pull/46)
- Add support for OPENAI_ORG_ID environment variable for setting the OpenAI organization ID when using an API key with multiple OpenAI organizations.
- Fix for unhelpful "Error getting plan, context, convo, or summaries" error message when OpenAI returns an error for invalid API key or insufficient credits (https://github.com/plandex-ai/plandex/issues/32)

## Version 0.8.2
- Fix for creating an org that auto-adds users based on email domain (https://github.com/plandex-ai/plandex/issues/24)
- Fix for possible crash after error in file build
- Added crash prevention measures across the board
- Fix for occasional "replacements failed" error
- Reliability and improvements for file updates
- Fix for role name of auto-continue model

## Version 0.8.1
- Fixes for two potential server crashes
- Fix for server git repo remaining in locked state after a crash, which caused various issues
- Fix for server git user and email not being set in some environments (https://github.com/plandex-ai/plandex/issues/8)
- Fix for 'replacements failed' error that was popping up in some circumstances
- Fix for build issue that could cause large updates to fail, take too long, or use too many tokens in some circumstances
- Clean up extraneous logging
- Prompt update to prevent ouputting files at absolute paths (like '/etc/config.txt')
- Prompt update to prevent sometimes using file block format for explanations, causing explanations to be outputted as files
- Prompt update to prevent stopping before the plan is really finished 
- Increase maximum number of auto-continuations to 50 (from 30)

## Version 0.8.0
- User management improvements and fixes
- Backend support for `plandex invite`, `plandex users`, and `plandex revoke` commands
- Improvements to copy for email verification emails
- Fix for org creation when creating a new account
- Send an email to invited user when they are invited to an org
- Add timeout when forwarding requests from one instance to another within a cluster

## Version 0.7.1
- Fix for SMTP email issue
- Add '/version' endpoint to server

## Version 0.7.0
Initial release
