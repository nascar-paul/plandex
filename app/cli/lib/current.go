package lib

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"plandex/api"
	"plandex/fs"
	"plandex/types"

	"github.com/plandex/plandex/shared"
)

var CurrentProjectId string
var CurrentPlanId string
var CurrentBranch string
var HomeCurrentProjectDir string
var HomeCurrentPlanPath string

func MustResolveProject() {
	if fs.PlandexDir == "" {
		_, _, err := fs.FindOrCreatePlandex()
		if err != nil {
			panic(fmt.Errorf("error finding or creating plandex: %v", err))
		}
	}

	if fs.PlandexDir == "" || fs.ProjectRoot == "" {
		panic(fmt.Errorf("could not find or create plandex directory"))
	}

	// check if project.json exists in PlandexDir
	path := filepath.Join(fs.PlandexDir, "project.json")
	_, err := os.Stat(path)

	if os.IsNotExist(err) {
		mustInitProject()
	} else if err != nil {
		panic(fmt.Errorf("error checking if project.json exists: %v", err))
	}

	// read project.json
	bytes, err := os.ReadFile(path)

	if err != nil {
		panic(fmt.Errorf("error reading project.json: %v", err))
	}

	var settings types.CurrentProjectSettings
	err = json.Unmarshal(bytes, &settings)

	if err != nil {
		panic(fmt.Errorf("error unmarshalling project.json: %v", err))
	}

	CurrentProjectId = settings.Id

	HomeCurrentProjectDir = filepath.Join(fs.HomePlandexDir, CurrentProjectId)
	HomeCurrentPlanPath = filepath.Join(HomeCurrentProjectDir, "current_plan.json")

	err = os.MkdirAll(HomeCurrentProjectDir, os.ModePerm)

	if err != nil {
		panic(fmt.Errorf("error creating project dir: %v", err))
	}

	MustLoadCurrentPlan()
}

func MustLoadCurrentPlan() {
	if CurrentProjectId == "" {
		panic("No current project")
	}

	// Check if the file exists
	_, err := os.Stat(HomeCurrentPlanPath)

	if os.IsNotExist(err) {
		return
	} else if err != nil {
		panic(fmt.Errorf("error checking if current_plan.json exists: %v", err))
	}

	// Read the contents of the file
	fileBytes, err := os.ReadFile(HomeCurrentPlanPath)
	if err != nil {
		panic(fmt.Errorf("error reading current_plan.json: %v", err))
	}

	var currentPlan types.CurrentPlanSettings
	err = json.Unmarshal(fileBytes, &currentPlan)
	if err != nil {
		panic(fmt.Errorf("error unmarshalling current_plan.json: %v", err))
	}

	CurrentPlanId = currentPlan.Id

	err = loadCurrentBranch()

	if err != nil {
		panic(fmt.Errorf("error loading current branch: %v", err))
	}

	if CurrentBranch == "" {
		err = WriteCurrentBranch("main")

		if err != nil {
			panic(fmt.Errorf("error setting current branch: %v", err))
		}
	}
}

func loadCurrentBranch() error {
	// Load plan-specific settings
	if CurrentPlanId == "" {
		return fmt.Errorf("no current plan")
	}

	path := filepath.Join(HomeCurrentProjectDir, CurrentPlanId, "settings.json")

	// Check if the file exists
	_, err := os.Stat(path)

	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("error checking if settings.json exists: %v", err)
	}

	fileBytes, err := os.ReadFile(path)
	if err != nil {
		panic(fmt.Errorf("error reading settings.json: %v", err))
	}

	var settings types.PlanSettings
	err = json.Unmarshal(fileBytes, &settings)
	if err != nil {
		panic(fmt.Errorf("error unmarshalling settings.json: %v", err))
	}

	CurrentBranch = settings.Branch

	return nil
}

func mustInitProject() {
	res, apiErr := api.Client.CreateProject(shared.CreateProjectRequest{Name: filepath.Base(fs.ProjectRoot)})

	if apiErr != nil {
		panic(fmt.Errorf("error creating project: %v", apiErr.Msg))
	}

	CurrentProjectId = res.Id

	// write project.json
	path := filepath.Join(fs.PlandexDir, "project.json")
	bytes, err := json.Marshal(types.CurrentProjectSettings{
		Id: CurrentProjectId,
	})

	if err != nil {
		panic(fmt.Errorf("error marshalling project settings: %v", err))
	}

	err = os.WriteFile(path, bytes, os.ModePerm)

	if err != nil {
		panic(fmt.Errorf("error writing project.json: %v", err))
	}

	// write current_plan.json to PlandexHomeDir/[projectId]/current_plan.json
	dir := filepath.Join(fs.HomePlandexDir, CurrentProjectId)
	err = os.MkdirAll(dir, os.ModePerm)

	if err != nil {
		panic(fmt.Errorf("error creating project dir: %v", err))
	}

	path = filepath.Join(dir, "current_plan.json")
	bytes, err = json.Marshal(types.CurrentPlanSettings{
		Id: "",
	})

	if err != nil {
		panic(fmt.Errorf("error marshalling plan settings: %v", err))
	}

	err = os.WriteFile(path, bytes, os.ModePerm)

	if err != nil {
		panic(fmt.Errorf("error writing current_plan.json: %v", err))
	}
}
