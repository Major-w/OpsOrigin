package version

import (
	"fmt"
	"time"
)

// VersionStatus 版本状态
type VersionStatus string

const (
	StatusDraft     VersionStatus = "draft"     // 草稿
	StatusPublished VersionStatus = "published" // 已发布
	StatusArchived  VersionStatus = "archived"  // 已归档
)

// PageVersion 页面版本
type PageVersion struct {
	ID          string        `json:"id"`
	PageID      string        `json:"page_id"`
	Version     string        `json:"version"`      // 语义化版本号: 1.0.0
	VersionNum  int           `json:"version_num"`  // 版本序号
	Status      VersionStatus `json:"status"`
	Content     []ComponentNode `json:"content"`    // 页面内容快照
	ChangeLog   string        `json:"change_log"`   // 变更日志
	CreatedBy   string        `json:"created_by"`
	PublishedBy string        `json:"published_by,omitempty"`
	PublishedAt *time.Time    `json:"published_at,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

// ComponentNode 组件节点（用于版本快照）
type ComponentNode struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Label      string                 `json:"label"`
	Props      map[string]interface{} `json:"props"`
	Children   []ComponentNode        `json:"children,omitempty"`
	SortOrder  int                    `json:"sort_order"`
}

// ModelVersion 数据模型版本
type ModelVersion struct {
	ID          string        `json:"id"`
	ModelID     string        `json:"model_id"`
	Version     string        `json:"version"`
	VersionNum  int           `json:"version_num"`
	Status      VersionStatus `json:"status"`
	Schema      []FieldSchema `json:"schema"`      // 模型Schema快照
	ChangeLog   string        `json:"change_log"`
	CreatedBy   string        `json:"created_by"`
	PublishedBy string        `json:"published_by,omitempty"`
	PublishedAt *time.Time    `json:"published_at,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

// FieldSchema 字段Schema
type FieldSchema struct {
	Name         string                 `json:"name"`
	Label        string                 `json:"label"`
	Type         string                 `json:"type"`
	Required     bool                   `json:"required"`
	Unique       bool                   `json:"unique"`
	DefaultValue interface{}            `json:"default_value,omitempty"`
	Options      map[string]interface{} `json:"options,omitempty"`
}

// VersionService 版本管理服务
type VersionService struct {
	pageVersions  map[string][]PageVersion
	modelVersions map[string][]ModelVersion
}

// NewVersionService 创建版本管理服务
func NewVersionService() *VersionService {
	return &VersionService{
		pageVersions:  make(map[string][]PageVersion),
		modelVersions: make(map[string][]ModelVersion),
	}
}

// CreatePageVersion 创建页面版本
func (s *VersionService) CreatePageVersion(pageID, createdBy, changeLog string, content []ComponentNode) (*PageVersion, error) {
	versions := s.pageVersions[pageID]
	versionNum := len(versions) + 1
	version := fmt.Sprintf("1.0.%d", versionNum-1)

	now := time.Now()
	pageVersion := PageVersion{
		ID:         fmt.Sprintf("pv-%s-%d", pageID, versionNum),
		PageID:     pageID,
		Version:    version,
		VersionNum: versionNum,
		Status:     StatusDraft,
		Content:    content,
		ChangeLog:  changeLog,
		CreatedBy:  createdBy,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	s.pageVersions[pageID] = append(s.pageVersions[pageID], pageVersion)
	return &pageVersion, nil
}

// GetPageVersions 获取页面所有版本
func (s *VersionService) GetPageVersions(pageID string) []PageVersion {
	return s.pageVersions[pageID]
}

// GetPageVersion 获取页面指定版本
func (s *VersionService) GetPageVersion(pageID, versionID string) (*PageVersion, error) {
	versions := s.pageVersions[pageID]
	for _, v := range versions {
		if v.ID == versionID {
			return &v, nil
		}
	}
	return nil, fmt.Errorf("version not found: %s", versionID)
}

// PublishPageVersion 发布页面版本
func (s *VersionService) PublishPageVersion(pageID, versionID, publishedBy string) error {
	versions := s.pageVersions[pageID]
	for i, v := range versions {
		if v.ID == versionID {
			// 将其他版本设为归档
			for j, other := range versions {
				if other.Status == StatusPublished {
					versions[j].Status = StatusArchived
					versions[j].UpdatedAt = time.Now()
				}
			}
			// 发布当前版本
			now := time.Now()
			versions[i].Status = StatusPublished
			versions[i].PublishedBy = publishedBy
			versions[i].PublishedAt = &now
			versions[i].UpdatedAt = now
			s.pageVersions[pageID] = versions
			return nil
		}
	}
	return fmt.Errorf("version not found: %s", versionID)
}

// RollbackPageVersion 回滚到指定版本
func (s *VersionService) RollbackPageVersion(pageID, versionID, operatorID string) (*PageVersion, error) {
	targetVersion, err := s.GetPageVersion(pageID, versionID)
	if err != nil {
		return nil, err
	}

	// 创建新版本，内容与目标版本相同
	newVersion, err := s.CreatePageVersion(pageID, operatorID, 
		fmt.Sprintf("Rollback to version %s", targetVersion.Version), targetVersion.Content)
	if err != nil {
		return nil, err
	}

	// 发布新版本
	err = s.PublishPageVersion(pageID, newVersion.ID, operatorID)
	if err != nil {
		return nil, err
	}

	return newVersion, nil
}

// CreateModelVersion 创建模型版本
func (s *VersionService) CreateModelVersion(modelID, createdBy, changeLog string, schema []FieldSchema) (*ModelVersion, error) {
	versions := s.modelVersions[modelID]
	versionNum := len(versions) + 1
	version := fmt.Sprintf("1.0.%d", versionNum-1)

	now := time.Now()
	modelVersion := ModelVersion{
		ID:         fmt.Sprintf("mv-%s-%d", modelID, versionNum),
		ModelID:    modelID,
		Version:    version,
		VersionNum: versionNum,
		Status:     StatusDraft,
		Schema:     schema,
		ChangeLog:  changeLog,
		CreatedBy:  createdBy,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	s.modelVersions[modelID] = append(s.modelVersions[modelID], modelVersion)
	return &modelVersion, nil
}

// GetModelVersions 获取模型所有版本
func (s *VersionService) GetModelVersions(modelID string) []ModelVersion {
	return s.modelVersions[modelID]
}

// GetModelVersion 获取模型指定版本
func (s *VersionService) GetModelVersion(modelID, versionID string) (*ModelVersion, error) {
	versions := s.modelVersions[modelID]
	for _, v := range versions {
		if v.ID == versionID {
			return &v, nil
		}
	}
	return nil, fmt.Errorf("version not found: %s", versionID)
}

// PublishModelVersion 发布模型版本
func (s *VersionService) PublishModelVersion(modelID, versionID, publishedBy string) error {
	versions := s.modelVersions[modelID]
	for i, v := range versions {
		if v.ID == versionID {
			// 将其他版本设为归档
			for j, other := range versions {
				if other.Status == StatusPublished {
					versions[j].Status = StatusArchived
					versions[j].UpdatedAt = time.Now()
				}
			}
			// 发布当前版本
			now := time.Now()
			versions[i].Status = StatusPublished
			versions[i].PublishedBy = publishedBy
			versions[i].PublishedAt = &now
			versions[i].UpdatedAt = now
			s.modelVersions[modelID] = versions
			return nil
		}
	}
	return fmt.Errorf("version not found: %s", versionID)
}

// RollbackModelVersion 回滚到指定版本
func (s *VersionService) RollbackModelVersion(modelID, versionID, operatorID string) (*ModelVersion, error) {
	targetVersion, err := s.GetModelVersion(modelID, versionID)
	if err != nil {
		return nil, err
	}

	// 创建新版本，Schema与目标版本相同
	newVersion, err := s.CreateModelVersion(modelID, operatorID,
		fmt.Sprintf("Rollback to version %s", targetVersion.Version), targetVersion.Schema)
	if err != nil {
		return nil, err
	}

	// 发布新版本
	err = s.PublishModelVersion(modelID, newVersion.ID, operatorID)
	if err != nil {
		return nil, err
	}

	return newVersion, nil
}