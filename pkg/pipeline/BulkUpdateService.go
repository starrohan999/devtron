package pipeline

import (
	"encoding/json"
	"github.com/devtron-labs/devtron/client/argocdServer/repository"
	repository3 "github.com/devtron-labs/devtron/internal/sql/repository"
	"github.com/devtron-labs/devtron/internal/sql/repository/bulkUpdate"
	"github.com/devtron-labs/devtron/internal/sql/repository/chartConfig"
	"github.com/devtron-labs/devtron/internal/sql/repository/cluster"
	"github.com/devtron-labs/devtron/internal/sql/repository/pipelineConfig"
	"github.com/devtron-labs/devtron/internal/util"
	jsonpatch "github.com/evanphx/json-patch"
	"go.uber.org/zap"
	"net/http"
)

type NameIncludesExcludes struct {
	Names []string `json:"names"`
}
type Spec struct {
	PatchJson string `json:"patchJson"`
}
type Tasks struct {
	Spec Spec `json:"spec"`
}
type BulkUpdatePayload struct {
	Includes           NameIncludesExcludes `json:"includes"`
	Excludes           NameIncludesExcludes `json:"excludes"`
	EnvIds             []int                `json:"envIds"`
	Global             bool                 `json:"global"`
	DeploymentTemplate Tasks                `json:"deploymentTemplate"`
}
type BulkUpdateScript struct {
	ApiVersion string            `json:"apiVersion" validate:"required"`
	Kind       string            `json:"kind" validate:"required"`
	Spec       BulkUpdatePayload `json:"spec" validate:"required"`
}
type BulkUpdateResponse struct {
	Operation string           `json:"operation"`
	Script    BulkUpdateScript `json:"script" validate:"required"`
	ReadMe    string           `json:"readme"`
}

type ImpactedObjectsResponse struct {
	AppId   int    `json:"appId"`
	AppName string `json:"appName"`
	EnvId   int    `json:"envId"`
}

type BulkUpdateService interface {
	FindBulkUpdateReadme(operation string) (response BulkUpdateResponse, err error)
	GetBulkAppName(bulkUpdateRequest BulkUpdatePayload) ([]*ImpactedObjectsResponse, error)
	ApplyJsonPatch(patch jsonpatch.Patch, target string) (string, error)
	BulkUpdateDeploymentTemplate(bulkUpdateRequest BulkUpdatePayload) (response string, err error)
}

type BulkUpdateServiceImpl struct {
	bulkUpdateRepository      bulkUpdate.BulkUpdateRepository
	chartRepository           chartConfig.ChartRepository
	logger                    *zap.SugaredLogger
	repoRepository            chartConfig.ChartRepoRepository
	chartTemplateService      util.ChartTemplateService
	pipelineGroupRepository   pipelineConfig.AppRepository
	mergeUtil                 util.MergeUtil
	repositoryService         repository.ServiceClient
	refChartDir               RefChartDir
	defaultChart              DefaultChart
	chartRefRepository        chartConfig.ChartRefRepository
	envOverrideRepository     chartConfig.EnvConfigOverrideRepository
	pipelineConfigRepository  chartConfig.PipelineConfigRepository
	configMapRepository       chartConfig.ConfigMapRepository
	environmentRepository     cluster.EnvironmentRepository
	pipelineRepository        pipelineConfig.PipelineRepository
	appLevelMetricsRepository repository3.AppLevelMetricsRepository
	client                    *http.Client
}

func NewBulkUpdateServiceImpl(bulkUpdateRepository bulkUpdate.BulkUpdateRepository,
	chartRepository chartConfig.ChartRepository,
	logger *zap.SugaredLogger,
	chartTemplateService util.ChartTemplateService,
	repoRepository chartConfig.ChartRepoRepository,
	pipelineGroupRepository pipelineConfig.AppRepository,
	refChartDir RefChartDir,
	defaultChart DefaultChart,
	mergeUtil util.MergeUtil,
	repositoryService repository.ServiceClient,
	chartRefRepository chartConfig.ChartRefRepository,
	envOverrideRepository chartConfig.EnvConfigOverrideRepository,
	pipelineConfigRepository chartConfig.PipelineConfigRepository,
	configMapRepository chartConfig.ConfigMapRepository,
	environmentRepository cluster.EnvironmentRepository,
	pipelineRepository pipelineConfig.PipelineRepository,
	appLevelMetricsRepository repository3.AppLevelMetricsRepository,
	client *http.Client,
) *BulkUpdateServiceImpl {
	return &BulkUpdateServiceImpl{
		bulkUpdateRepository:      bulkUpdateRepository,
		chartRepository:           chartRepository,
		logger:                    logger,
		chartTemplateService:      chartTemplateService,
		repoRepository:            repoRepository,
		pipelineGroupRepository:   pipelineGroupRepository,
		mergeUtil:                 mergeUtil,
		refChartDir:               refChartDir,
		defaultChart:              defaultChart,
		repositoryService:         repositoryService,
		chartRefRepository:        chartRefRepository,
		envOverrideRepository:     envOverrideRepository,
		pipelineConfigRepository:  pipelineConfigRepository,
		configMapRepository:       configMapRepository,
		environmentRepository:     environmentRepository,
		pipelineRepository:        pipelineRepository,
		appLevelMetricsRepository: appLevelMetricsRepository,
		client:                    client,
	}
}
func (impl BulkUpdateServiceImpl) FindBulkUpdateReadme(operation string) (BulkUpdateResponse, error) {
	bulkUpdateReadme, err := impl.bulkUpdateRepository.FindBulkUpdateReadme(operation)
	response := BulkUpdateResponse{}
	if err != nil {
		impl.logger.Errorw("error in fetching batch operation example", "err", err)
		return response, err
	}
	script := BulkUpdateScript{}
	err = json.Unmarshal([]byte(bulkUpdateReadme.Script), &script)
	if err != nil {
		impl.logger.Errorw("error in script value(in db) of batch operation example", "err", err)
		return response, err
	}
	response = BulkUpdateResponse{
		Operation: bulkUpdateReadme.Operation,
		Script:    script,
		ReadMe:    bulkUpdateReadme.Readme,
	}
	return response, nil
}
func (impl BulkUpdateServiceImpl) GetBulkAppName(bulkUpdatePayload BulkUpdatePayload) ([]*ImpactedObjectsResponse, error) {
	impactedObjectsResponse := []*ImpactedObjectsResponse{}
	AppTrackMap := make(map[int]string)
	if bulkUpdatePayload.Global {
		appsGlobal, err := impl.bulkUpdateRepository.
			FindBulkAppNameForGlobal(bulkUpdatePayload.Includes.Names, bulkUpdatePayload.Excludes.Names)
		if err != nil {
			impl.logger.Errorw("error in fetching bulk app names for global", "err", err)
			return nil, err
		}
		for _, app := range appsGlobal {
			if _, AppAlreadyAddedToResponse := AppTrackMap[app.Id]; AppAlreadyAddedToResponse {
				continue
			}
			AppTrackMap[app.Id] = app.AppName
			impactedObject := &ImpactedObjectsResponse{
				AppId:   app.Id,
				AppName: app.AppName,
			}
			impactedObjectsResponse = append(impactedObjectsResponse, impactedObject)
		}
	}
	for _, envId := range bulkUpdatePayload.EnvIds {
		appsNotGlobal, err := impl.bulkUpdateRepository.
			FindBulkAppNameForEnv(bulkUpdatePayload.Includes.Names, bulkUpdatePayload.Excludes.Names, envId)
		if err != nil {
			impl.logger.Errorw("error in fetching bulk app names for env", "err", err)
			return nil, err
		}
		for _, app := range appsNotGlobal {
			if _, AppAlreadyAddedToResponse := AppTrackMap[app.Id]; AppAlreadyAddedToResponse {
				continue
			}
			AppTrackMap[app.Id] = app.AppName
			impactedObject := &ImpactedObjectsResponse{
				AppId:   app.Id,
				AppName: app.AppName,
				EnvId:   envId,
			}
			impactedObjectsResponse = append(impactedObjectsResponse, impactedObject)
		}
	}
	return impactedObjectsResponse, nil
}
func (impl BulkUpdateServiceImpl) ApplyJsonPatch(patch jsonpatch.Patch, target string) (string, error) {
	modified, err := patch.Apply([]byte(target))
	if err != nil {
		impl.logger.Error("error in applying JSON patch")
		return "Patch Failed", err
	}
	return string(modified), err
}
func (impl BulkUpdateServiceImpl) BulkUpdateDeploymentTemplate(bulkUpdatePayload BulkUpdatePayload) (string, error) {
	patchJson := []byte(bulkUpdatePayload.DeploymentTemplate.Spec.PatchJson)
	patch, err := jsonpatch.DecodePatch(patchJson)
	FailureMessage := "Bulk Update Failed"
	SuccessMessage := "Bulk Update is successful"
	if err != nil {
		impl.logger.Errorw("error in decoding JSON patch", "err", err)
		return FailureMessage, err
	}
	UpdatedPatchMap := make(map[int]string)
	if bulkUpdatePayload.Global {
		charts, err := impl.bulkUpdateRepository.FindBulkChartsByAppNameSubstring(bulkUpdatePayload.Includes.Names, bulkUpdatePayload.Excludes.Names)
		if err != nil {
			impl.logger.Error("error in fetching charts by app name substring")
			return FailureMessage, err
		}
		for _, chart := range charts {
			modified, err := impl.ApplyJsonPatch(patch, chart.Values)
			if err != nil {
				impl.logger.Error("error in applying JSON patch")
				return FailureMessage, err
			}
			UpdatedPatchMap[chart.Id] = modified
		}
		err = impl.bulkUpdateRepository.BulkUpdateChartsValuesYamlAndGlobalOverrideById(UpdatedPatchMap)
		if err != nil {
			impl.logger.Error("error in bulk updating charts")
			return FailureMessage, err
		}
	}
	for _, envId := range bulkUpdatePayload.EnvIds {
		chartsEnv, err := impl.bulkUpdateRepository.FindBulkChartsEnvByAppNameSubstring(bulkUpdatePayload.Includes.Names, bulkUpdatePayload.Excludes.Names, envId)
		if err != nil {
			impl.logger.Errorw("error in fetching charts(for env) by app name substring", "err", err)
			return FailureMessage, err
		}
		for _, chartEnv := range chartsEnv {
			modified, err := impl.ApplyJsonPatch(patch, chartEnv.EnvOverrideValues)
			if err != nil {
				impl.logger.Errorw("error in applying json patch", "err", err)
				return FailureMessage, err
			}
			UpdatedPatchMap[chartEnv.Id] = modified
		}
		err = impl.bulkUpdateRepository.BulkUpdateChartsEnvYamlOverrideById(UpdatedPatchMap)
		if err != nil {
			impl.logger.Errorw("error in bulk updating charts(for env)", "err", err)
			return FailureMessage, err
		}
	}
	return SuccessMessage, err
}