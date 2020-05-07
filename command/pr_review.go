package command

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/spf13/cobra"

	"github.com/cli/cli/api"
	"github.com/cli/cli/pkg/surveyext"
	"github.com/cli/cli/utils"
)

func init() {
	prCmd.AddCommand(prReviewCmd)

	prReviewCmd.Flags().StringP("approve", "a", "", "Approve pull request")
	prReviewCmd.Flags().StringP("request-changes", "r", "", "Request changes on a pull request")
	prReviewCmd.Flags().StringP("comment", "c", "", "Comment on a pull request")

	// this is required; without it pflag complains that you must pass a string to string flags.
	prReviewCmd.Flags().Lookup("approve").NoOptDefVal = " "
	prReviewCmd.Flags().Lookup("request-changes").NoOptDefVal = " "
	prReviewCmd.Flags().Lookup("comment").NoOptDefVal = " "
}

var prReviewCmd = &cobra.Command{
	Use:   "review",
	Short: "TODO",
	Args:  cobra.MaximumNArgs(1),
	Long:  "TODO",
	RunE:  prReview,
}

func processReviewOpt(cmd *cobra.Command) (*api.PullRequestReviewInput, error) {
	found := 0
	flag := ""
	var state api.PullRequestReviewState

	if cmd.Flags().Changed("approve") {
		found++
		flag = "approve"
		state = api.ReviewApprove
	}
	if cmd.Flags().Changed("request-changes") {
		found++
		flag = "request-changes"
		state = api.ReviewRequestChanges
	}
	if cmd.Flags().Changed("comment") {
		found++
		flag = "comment"
		state = api.ReviewComment
	}

	if found == 0 {
		return nil, nil // signal interactive mode
	} else if found > 1 {
		return nil, errors.New("need exactly one of --approve, --request-changes, or --comment")
	}

	val, err := cmd.Flags().GetString(flag)
	if err != nil {
		return nil, err
	}

	body := ""
	if val != " " {
		body = val
	}

	if flag == "comment" && (body == " " || len(body) == 0) {
		return nil, errors.New("cannot leave blank comment")
	}

	return &api.PullRequestReviewInput{
		Body:  body,
		State: state,
	}, nil
}

func prReview(cmd *cobra.Command, args []string) error {
	ctx := contextForCommand(cmd)
	baseRepo, err := determineBaseRepo(cmd, ctx)
	if err != nil {
		return fmt.Errorf("could not determine base repo: %w", err)
	}

	apiClient, err := apiClientForContext(ctx)
	if err != nil {
		return err
	}

	var prNum int
	branchWithOwner := ""

	if len(args) == 0 {
		prNum, branchWithOwner, err = prSelectorForCurrentBranch(ctx, baseRepo)
		if err != nil {
			return fmt.Errorf("could not query for pull request for current branch: %w", err)
		}
	} else {
		prArg, repo := prFromURL(args[0])
		if repo != nil {
			baseRepo = repo
			// TODO handle malformed URL; it falls through to Atoi
		} else {
			prArg = strings.TrimPrefix(args[0], "#")
		}
		prNum, err = strconv.Atoi(prArg)
		if err != nil {
			return fmt.Errorf("could not parse pull request number: %w", err)
		}
	}

	input, err := processReviewOpt(cmd)
	if err != nil {
		return fmt.Errorf("did not understand desired review action: %w", err)
	}

	var pr *api.PullRequest
	if prNum > 0 {
		pr, err = api.PullRequestByNumber(apiClient, baseRepo, prNum)
		if err != nil {
			return fmt.Errorf("could not find pull request: %w", err)
		}
	} else {
		pr, err = api.PullRequestForBranch(apiClient, baseRepo, "", branchWithOwner)
		if err != nil {
			return fmt.Errorf("could not find pull request: %w", err)
		}
	}

	if input == nil {
		input, err = reviewSurvey(cmd)
		if err != nil {
			return err
		}
		if input == nil && err == nil {
			// Cancelled.
			return nil
		}
	}

	err = api.AddReview(apiClient, pr, input)
	if err != nil {
		return fmt.Errorf("failed to create review: %w", err)
	}

	return nil
}

func reviewSurvey(cmd *cobra.Command) (*api.PullRequestReviewInput, error) {
	editorCommand, err := determineEditor(cmd)
	if err != nil {
		return nil, err
	}

	typeAnswers := struct {
		ReviewType string
	}{}
	typeQs := []*survey.Question{
		{
			Name: "reviewType",
			Prompt: &survey.Select{
				Message: "What kind of review do you want to create?",
				Options: []string{
					"Comment",
					"Approve",
					"Request Changes",
					"Cancel",
				},
			},
		},
	}

	err = SurveyAsk(typeQs, &typeAnswers)
	if err != nil {
		return nil, err
	}

	reviewState := api.ReviewComment

	switch typeAnswers.ReviewType {
	case "Approve":
		reviewState = api.ReviewApprove
	case "Request Changes":
		reviewState = api.ReviewRequestChanges
	case "Cancel":
		return nil, nil
	}

	bodyAnswers := struct {
		Body string
	}{}

	bodyQs := []*survey.Question{
		&survey.Question{
			Name: "body",
			Prompt: &surveyext.GhEditor{
				EditorCommand: editorCommand,
				Editor: &survey.Editor{
					Message:  "Review Body",
					FileName: "*.md",
				},
			},
		},
	}

	err = SurveyAsk(bodyQs, &bodyAnswers)
	if err != nil {
		return nil, err
	}

	if reviewState == api.ReviewComment && bodyAnswers.Body == "" {
		return nil, errors.New("cannot leave blank comment")
	}

	if len(bodyAnswers.Body) > 0 {
		out := colorableOut(cmd)
		renderedBody, err := utils.RenderMarkdown(bodyAnswers.Body)
		if err != nil {
			return nil, err
		}

		fmt.Fprintf(out, "Got:\n%s", renderedBody)
	}

	confirmAnswers := struct {
		Confirm string
	}{}
	confirmQs := []*survey.Question{
		{
			Name: "confirm",
			Prompt: &survey.Select{
				Message: "What's next?",
				Options: []string{
					"Submit",
					"Cancel",
				},
			},
		},
	}

	err = SurveyAsk(confirmQs, &confirmAnswers)
	if err != nil {
		return nil, err
	}

	if confirmAnswers.Confirm == "Cancel" {
		return nil, nil
	}

	return &api.PullRequestReviewInput{
		Body:  bodyAnswers.Body,
		State: reviewState,
	}, nil
}
