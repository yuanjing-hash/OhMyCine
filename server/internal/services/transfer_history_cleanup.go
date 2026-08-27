package services

import (
	"errors"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"gorm.io/gorm"
)

// cleanupTransferHistoryDependencies removes only OhMyCine's history and
// ownership bookkeeping for a terminal transfer. It never touches provider or
// filesystem content. Reorganization jobs must be terminal so deleting a
// transfer cannot invalidate an active worker's ownership boundary.
func cleanupTransferHistoryDependencies(tx *gorm.DB, transferTaskID string) ([]models.Job, error) {
	var reorganizationTasks []models.MediaReorganizationTask
	if err := tx.Where("transfer_task_id = ?", transferTaskID).Find(&reorganizationTasks).Error; err != nil {
		return nil, err
	}
	reorganizationJobs := make([]models.Job, 0, len(reorganizationTasks))
	for _, task := range reorganizationTasks {
		var job models.Job
		if err := tx.First(&job, "id = ?", task.JobID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, appError(CodeQueueStateConflict, "重新整理历史不完整，不能删除媒体整理记录", nil)
			}
			return nil, err
		}
		if !isTerminalPipelineJobStatus(job.Status) {
			return nil, appError(CodeQueueStateConflict, "重新整理任务仍在执行，不能删除媒体整理记录", nil)
		}
		if jobHasActiveLease(job, time.Now().UTC()) {
			return nil, appError(CodeQueueStateConflict, "重新整理 worker 仍在收口，不能删除媒体整理记录", nil)
		}
		reorganizationJobs = append(reorganizationJobs, job)
	}
	if err := tx.Where("transfer_task_id = ?", transferTaskID).Delete(&models.MediaReorganizationPreview{}).Error; err != nil {
		return nil, err
	}
	if err := tx.Where("transfer_task_id = ?", transferTaskID).Delete(&models.MediaManagedItem{}).Error; err != nil {
		return nil, err
	}
	if err := tx.Where("transfer_task_id = ?", transferTaskID).Delete(&models.MediaReorganizationTask{}).Error; err != nil {
		return nil, err
	}
	if len(reorganizationJobs) > 0 {
		ids := make([]string, 0, len(reorganizationJobs))
		for _, job := range reorganizationJobs {
			ids = append(ids, job.ID)
		}
		if err := tx.Where("id IN ?", ids).Delete(&models.Job{}).Error; err != nil {
			return nil, err
		}
	}
	return reorganizationJobs, nil
}

func isTerminalPipelineJobStatus(status string) bool {
	return status == models.JobStatusCompleted || status == models.JobStatusFailed || status == models.JobStatusCancelled
}
