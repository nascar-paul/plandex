package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"plandex-server/db"
	"plandex-server/model"
	"plandex-server/model/prompts"
	"plandex-server/types"
	"sort"
	"strings"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/google/uuid"
	"github.com/plandex/plandex/shared"
	"github.com/sashabaranov/go-openai"
)

const MaxRetries = 3
const MaxReplacementRetries = 1

func QueueBuild(client *openai.Client, currentOrgId, currentUserId, planId, branch string, activeBuild *types.ActiveBuild) {
	activePlan := GetActivePlan(planId, branch)
	filePath := activeBuild.Path

	UpdateActivePlan(planId, branch, func(active *types.ActivePlan) {
		active.BuildQueuesByPath[filePath] = append(active.BuildQueuesByPath[filePath], activeBuild)
	})
	log.Printf("Queued build for file %s\n", filePath)
	log.Printf("Queue:")
	spew.Dump(activePlan.BuildQueuesByPath[filePath])

	if activePlan.IsBuildingByPath[filePath] {
		log.Printf("Already building file %s\n", filePath)
		return
	} else {
		log.Printf("Will process build queue for file %s\n", filePath)
		go execPlanBuild(client, currentOrgId, currentUserId, branch, activePlan, []*types.ActiveBuild{activeBuild})
	}
}

func execPlanBuild(client *openai.Client, currentOrgId, currentUserId, branch string, activePlan *types.ActivePlan, activeBuilds []*types.ActiveBuild) {
	if len(activeBuilds) == 0 {
		log.Println("No active builds")
		return
	}

	// all builds should have the same path
	filePath := activeBuilds[0].Path
	planId := activePlan.Id
	var convoMessageIds []string
	added := map[string]bool{}

	for _, activeBuild := range activeBuilds {
		if !added[activeBuild.AssistantMessageId] {
			convoMessageIds = append(convoMessageIds, activeBuild.AssistantMessageId)
			added[activeBuild.AssistantMessageId] = true
		}
	}

	UpdateActivePlan(activePlan.Id, activePlan.Branch, func(ap *types.ActivePlan) {
		ap.IsBuildingByPath[filePath] = true
	})

	buildInfo := &shared.BuildInfo{
		Path:      filePath,
		NumTokens: 0,
		Finished:  false,
	}
	activePlan.Stream(shared.StreamMessage{
		Type:      shared.StreamMessageBuildInfo,
		BuildInfo: buildInfo,
	})

	errCh := make(chan error)

	var build *db.PlanBuild
	var currentPlan *shared.CurrentPlanState

	go func() {
		build = &db.PlanBuild{
			OrgId:           currentOrgId,
			PlanId:          planId,
			ConvoMessageIds: convoMessageIds,
			FilePath:        filePath,
		}
		err := db.StorePlanBuild(build)

		if err != nil {
			errCh <- fmt.Errorf("error storing plan build: %v", err)
			return
		}

		errCh <- nil
	}()

	go func() {
		res, err := db.GetCurrentPlanState(db.CurrentPlanStateParams{
			OrgId:  currentOrgId,
			PlanId: planId,
		})
		if err != nil {
			errCh <- fmt.Errorf("error getting current plan state: %v", err)
			return
		}
		currentPlan = res
		errCh <- nil
	}()

	for i := 0; i < 2; i++ {
		err := <-errCh
		if err != nil {
			log.Printf("Error building plan %s: %v\n", planId, err)
			UpdateActivePlan(activePlan.Id, activePlan.Branch, func(ap *types.ActivePlan) {
				ap.IsBuildingByPath[filePath] = false
			})
			activePlan.StreamDoneCh <- &shared.ApiError{
				Type:   shared.ApiErrorTypeOther,
				Status: http.StatusInternalServerError,
				Msg:    err.Error(),
			}
			return
		}
	}

	onFinishBuild := func() {
		log.Println("Build finished")

		// get fresh current plan state
		currentPlan, err := db.GetCurrentPlanState(db.CurrentPlanStateParams{
			OrgId:  currentOrgId,
			PlanId: planId,
		})
		if err != nil {
			errCh <- fmt.Errorf("error getting current plan state: %v", err)
			return
		}

		err = db.GitAddAndAmendCommit(currentOrgId, planId, branch, currentPlan.PendingChangesSummary())

		if err != nil {
			log.Printf("Error committing plan build: %v\n", err)
			activePlan.StreamDoneCh <- &shared.ApiError{
				Type:   shared.ApiErrorTypeOther,
				Status: http.StatusInternalServerError,
				Msg:    "Error committing plan build: " + err.Error(),
			}
			return
		}

		if GetActivePlan(planId, branch).RepliesFinished {
			activePlan.Stream(shared.StreamMessage{
				Type: shared.StreamMessageFinished,
			})
		}
	}

	onFinishBuildFile := func(filePath string, planRes *db.PlanFileResult) {
		finished := false
		log.Println("onFinishBuildFile: " + filePath)

		repoLockId, err := db.LockRepo(
			db.LockRepoParams{
				OrgId:       currentOrgId,
				UserId:      currentUserId,
				PlanId:      planId,
				Branch:      branch,
				PlanBuildId: build.Id,
				Scope:       db.LockScopeWrite,
			},
		)
		if err != nil {
			log.Printf("Error locking repo for build file: %v\n", err)
			activePlan.StreamDoneCh <- &shared.ApiError{
				Type:   shared.ApiErrorTypeOther,
				Status: http.StatusInternalServerError,
				Msg:    "Error locking repo for build file: " + err.Error(),
			}
			return
		}

		err = func() error {
			var err error
			defer func() {
				if err != nil {
					log.Printf("Error: %v\n", err)
					err = db.GitClearUncommittedChanges(currentOrgId, planId)
					if err != nil {
						log.Printf("Error clearing uncommitted changes: %v\n", err)
					}
				}

				err := db.UnlockRepo(repoLockId)
				if err != nil {
					log.Printf("Error unlocking repo: %v\n", err)
				}
			}()

			err = db.StorePlanResult(planRes)
			if err != nil {
				log.Printf("Error storing plan result: %v\n", err)
				activePlan.StreamDoneCh <- &shared.ApiError{
					Type:   shared.ApiErrorTypeOther,
					Status: http.StatusInternalServerError,
					Msg:    "Error storing plan result: " + err.Error(),
				}
				return err
			}
			return nil
		}()

		if err != nil {
			return
		}

		for _, build := range activeBuilds {
			build.Success = true
		}

		UpdateActivePlan(planId, branch, func(ap *types.ActivePlan) {
			ap.BuiltFiles[filePath] = true
			ap.IsBuildingByPath[filePath] = false

			if ap.BuildFinished() {
				finished = true
			}
		})

		log.Printf("Finished building file %s\n", filePath)

		if finished {
			log.Println("Finished building plan, calling onFinishBuild")
			onFinishBuild()
		} else {
			if !activePlan.PathFinished(filePath) {
				log.Printf("Processing next build for file %s\n", filePath)
				var nextBuilds []*types.ActiveBuild
				for _, build := range activePlan.BuildQueuesByPath[filePath] {
					if !build.BuildFinished() && len(nextBuilds) < 5 {
						nextBuilds = append(nextBuilds, build)
					}
				}

				log.Println("Next builds:")
				spew.Dump(nextBuilds)

				if len(nextBuilds) > 0 {
					log.Println("Calling execPlanBuild for next build in queue")
					go execPlanBuild(client, currentOrgId, currentUserId, branch, activePlan, nextBuilds)
				}
				return
			} else {
				log.Printf("File %s finished, but not all builds finished\n", filePath)
			}
		}
	}

	onBuildFileError := func(filePath string, err error) {
		log.Printf("Error for file %s: %v\n", filePath, err)

		for _, build := range activeBuilds {
			build.Success = false
			build.Error = err
		}

		activePlan.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    err.Error(),
		}

		if err != nil {
			log.Printf("Error storing plan error result: %v\n", err)
		}

		build.Error = err.Error()

		err = db.SetBuildError(build)
		if err != nil {
			log.Printf("Error setting build error: %v\n", err)
		}

		UpdateActivePlan(activePlan.Id, activePlan.Branch, func(ap *types.ActivePlan) {
			ap.IsBuildingByPath[filePath] = false
		})
	}

	var buildFile func(filePath string, numRetry int, numReplacementRetry int, res *db.PlanFileResult)
	buildFile = func(filePath string, numRetry int, numReplacementsRetry int, res *db.PlanFileResult) {
		log.Printf("Building file %s, numRetry: %d\n", filePath, numRetry)

		// get relevant file context (if any)
		contextPart := activePlan.ContextsByPath[filePath]

		var currentState string
		currentPlanFile, fileInCurrentPlan := currentPlan.CurrentPlanFiles.Files[filePath]

		if fileInCurrentPlan {
			currentState = currentPlanFile

			log.Printf("File %s found in current plan. Using current state.\n", filePath)
			// log.Println("Current state:")
			// log.Println(currentState)
		} else if contextPart != nil {
			currentState = contextPart.Body
		}

		if currentState == "" {
			log.Printf("File %s not found in model context or current plan. Creating new file.\n", filePath)

			buildInfo := &shared.BuildInfo{
				Path:      filePath,
				NumTokens: 0,
				Finished:  true,
			}

			activePlan.Stream(shared.StreamMessage{
				Type:      shared.StreamMessageBuildInfo,
				BuildInfo: buildInfo,
			})

			// new file
			planRes := &db.PlanFileResult{
				OrgId:           currentOrgId,
				PlanId:          planId,
				PlanBuildId:     build.Id,
				ConvoMessageIds: build.ConvoMessageIds,
				Path:            filePath,
				Content:         activeBuilds[0].FileContent,
			}
			onFinishBuildFile(filePath, planRes)
			return
		}

		log.Println("Getting file from model: " + filePath)
		// log.Println("File context:", fileContext)

		replacePrompt := prompts.GetReplacePrompt(filePath)
		currentStatePrompt := prompts.GetBuildCurrentStatePrompt(filePath, currentState)
		sysPrompt := prompts.GetBuildSysPrompt(filePath, currentStatePrompt)

		var mergedReply string
		for _, activeBuild := range activeBuilds {
			mergedReply += "\n\n" + activeBuild.ReplyContent
		}

		log.Println("Num active builds: " + fmt.Sprintf("%d", len(activeBuilds)))
		log.Println("Merged reply:")
		log.Println(mergedReply)

		fileMessages := []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: sysPrompt,
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: activePlan.Prompt,
			},
			{
				Role:    openai.ChatMessageRoleAssistant,
				Content: mergedReply,
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: replacePrompt,
			},
		}

		if numReplacementsRetry > 0 && res != nil {
			bytes, err := json.Marshal(res.Replacements)
			if err != nil {
				onBuildFileError(filePath, fmt.Errorf("error marshalling replacements: %v", err))
				return
			}

			correctReplacementPrompt, err := prompts.GetCorrectReplacementPrompt(res.Replacements, currentState)
			if err != nil {
				onBuildFileError(filePath, fmt.Errorf("error getting correct replacement prompt: %v", err))
				return
			}

			fileMessages = append(fileMessages,
				openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleAssistant,
					Content: string(bytes),
				},

				openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleUser,
					Content: correctReplacementPrompt,
				})
		}

		log.Println("Calling model for file: " + filePath)

		// for _, msg := range fileMessages {
		// 	log.Printf("%s: %s\n", msg.Role, msg.Content)
		// }

		modelReq := openai.ChatCompletionRequest{
			Model:          model.BuilderModel,
			Functions:      []openai.FunctionDefinition{prompts.ReplaceFn},
			Messages:       fileMessages,
			Temperature:    0.2,
			TopP:           0.1,
			ResponseFormat: &openai.ChatCompletionResponseFormat{Type: "json_object"},
		}

		stream, err := client.CreateChatCompletionStream(activePlan.Ctx, modelReq)
		if err != nil {
			log.Printf("Error creating plan file stream for path '%s': %v\n", filePath, err)

			if numRetry >= MaxRetries {
				onBuildFileError(filePath, fmt.Errorf("failed to create plan file stream for path '%s' after %d retries: %v", filePath, numRetry, err))
			} else {
				log.Println("Retrying build plan for file: " + filePath)
				buildFile(filePath, numRetry+1, numReplacementsRetry, res)
				if err != nil {
					onBuildFileError(filePath, fmt.Errorf("failed to retry build plan for file '%s': %v", filePath, err))
				}
			}
			return
		}

		buffer := ""

		go func() {
			defer stream.Close()

			// Create a timer that will trigger if no chunk is received within the specified duration
			timer := time.NewTimer(model.OPENAI_STREAM_CHUNK_TIMEOUT)
			defer timer.Stop()

			handleErrorRetry := func(maxRetryErr error, shouldSleep bool, isReplacementsRetry bool, res *db.PlanFileResult) (shouldContinue bool) {
				log.Printf("Error for file %s: %v\n", filePath, maxRetryErr)

				if isReplacementsRetry && numReplacementsRetry >= MaxReplacementRetries {
					// in this case, we just want to ignore the error and continue
					return true
				} else if !isReplacementsRetry && numRetry >= MaxRetries {
					onBuildFileError(filePath, maxRetryErr)
					return false
				} else {
					if shouldSleep {
						time.Sleep(1 * time.Second * time.Duration(math.Pow(float64(numRetry+1), 2)))
					}
					if isReplacementsRetry {
						buildFile(filePath, numRetry+1, numReplacementsRetry+1, res)
					} else {
						buildFile(filePath, numRetry+1, numReplacementsRetry, res)
					}
					if err != nil {
						onBuildFileError(filePath, fmt.Errorf("failed to retry build plan for file '%s': %v", filePath, err))
					}
					return false
				}

			}

			for {
				select {
				case <-activePlan.Ctx.Done():
					// The main context was canceled (not the timer)
					return
				case <-timer.C:
					// Timer triggered because no new chunk was received in time
					handleErrorRetry(
						fmt.Errorf("stream timeout due to inactivity for file '%s' after %d retries", filePath, numRetry),
						true,
						false,
						res,
					)
					return
				default:
					response, err := stream.Recv()

					if err == nil {
						// Successfully received a chunk, reset the timer
						if !timer.Stop() {
							<-timer.C
						}
						timer.Reset(model.OPENAI_STREAM_CHUNK_TIMEOUT)
					} else {
						log.Printf("File %s: Error receiving stream chunk: %v\n", filePath, err)

						if err == context.Canceled {
							log.Printf("File %s: Stream canceled\n", filePath)
							return
						}

						handleErrorRetry(
							fmt.Errorf("stream error for file '%s' after %d retries: %v", filePath, numRetry, err),
							true,
							false,
							res,
						)
						return
					}

					if len(response.Choices) == 0 {
						handleErrorRetry(fmt.Errorf("stream error: no choices"), true, false, res)
						return
					}

					choice := response.Choices[0]

					if choice.FinishReason != "" {
						if choice.FinishReason != openai.FinishReasonFunctionCall {
							handleErrorRetry(
								fmt.Errorf("stream finished without a function call. Reason: %s, File: %s", choice.FinishReason, filePath),
								false,
								false,
								res,
							)
							return
						}

						log.Printf("File %s: Stream finished with non-function call\n", filePath)
						log.Println("finish reason: " + choice.FinishReason)

						active := GetActivePlan(planId, branch)
						if !active.BuiltFiles[filePath] {
							log.Printf("Stream finished before replacements parsed. File: %s\n", filePath)
							log.Println("Buffer:")
							log.Println(buffer)

							handleErrorRetry(
								fmt.Errorf("stream finished before replacements parsed. File: %s", filePath),
								true,
								false,
								res,
							)
							return
						}
					}

					var content string
					delta := response.Choices[0].Delta

					if delta.FunctionCall == nil {
						log.Println("No function call in delta. File:", filePath)
						spew.Dump(delta)
						continue
					} else {
						content = delta.FunctionCall.Arguments
					}

					buildInfo := &shared.BuildInfo{
						Path:      filePath,
						NumTokens: 1,
						Finished:  false,
					}

					// log.Printf("%s: %s", filePath, content)
					activePlan.Stream(shared.StreamMessage{
						Type:      shared.StreamMessageBuildInfo,
						BuildInfo: buildInfo,
					})

					buffer += content

					var streamed types.StreamedReplacements
					err = json.Unmarshal([]byte(buffer), &streamed)
					if err == nil && len(streamed.Replacements) > 0 {
						log.Printf("File %s: Parsed replacements\n", filePath)

						planFileResult, allSucceeded := getPlanResult(
							planResultParams{
								orgId:           currentOrgId,
								planId:          planId,
								planBuildId:     build.Id,
								convoMessageIds: build.ConvoMessageIds,
								filePath:        filePath,
								currentState:    currentState,
								context:         contextPart,
								replacements:    streamed.Replacements,
							},
						)

						// proposalId, filePath, currentState, contextPart, replacements.Replacements)

						if !allSucceeded {
							log.Println("Failed replacements:")
							for _, replacement := range planFileResult.Replacements {
								if replacement.Failed {
									spew.Dump(replacement)
								}
							}

							if numReplacementsRetry < MaxReplacementRetries {
								shouldContinue := handleErrorRetry(
									nil, // no error -- if we reach MAX_REPLACEMENT_RETRIES, we just ignore the error and continue
									false,
									true,
									planFileResult,
								)
								if !shouldContinue {
									return
								}
							}
						}

						buildInfo := &shared.BuildInfo{
							Path:      filePath,
							NumTokens: 0,
							Finished:  true,
						}
						activePlan.Stream(shared.StreamMessage{
							Type:      shared.StreamMessageBuildInfo,
							BuildInfo: buildInfo,
						})

						onFinishBuildFile(filePath, planFileResult)
						return
					}
				}
			}
		}()
	}

	buildFile(filePath, 0, 0, nil)
}

type planResultParams struct {
	orgId           string
	planId          string
	planBuildId     string
	convoMessageIds []string
	filePath        string
	currentState    string
	context         *db.Context
	replacements    []*shared.Replacement
}

func getPlanResult(params planResultParams) (*db.PlanFileResult, bool) {
	orgId := params.orgId
	planId := params.planId
	planBuildId := params.planBuildId
	filePath := params.filePath
	currentState := params.currentState
	contextPart := params.context
	replacements := params.replacements
	updated := params.currentState

	sort.Slice(replacements, func(i, j int) bool {
		iIdx := strings.Index(updated, replacements[i].Old)
		jIdx := strings.Index(updated, replacements[j].Old)
		return iIdx < jIdx
	})

	_, allSucceeded := shared.ApplyReplacements(currentState, replacements, true)

	var contextSha string
	if contextPart != nil {
		contextSha = contextPart.Sha
	}

	for _, replacement := range replacements {
		id := uuid.New().String()
		replacement.Id = id
	}

	return &db.PlanFileResult{
		OrgId:           orgId,
		PlanId:          planId,
		PlanBuildId:     planBuildId,
		ConvoMessageIds: params.convoMessageIds,
		Content:         "",
		Path:            filePath,
		Replacements:    replacements,
		ContextSha:      contextSha,
		ContextBody:     contextPart.Body,
		AnyFailed:       !allSucceeded,
	}, allSucceeded
}
