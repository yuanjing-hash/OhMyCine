import { api } from "@/api/client";
export type JobStatus =
  | "queued"
  | "running"
  | "waiting_user_action"
  | "retry_wait"
  | "paused"
  | "completed"
  | "failed"
  | "cancelled";
export interface JobAction {
  version: number;
  action_type: string;
  prompt: string;
  options: string[];
  preview: Record<string, string>;
  expires_at: string | null;
}
export interface Job {
  id: string;
  owner_id: number | null;
  created_by_kind: "user" | "system";
  job_type: string;
  priority: number;
  lane_position: number;
  lane_rank: number | null;
  revision: number;
  status: JobStatus;
  display_name: string;
  provider: string;
  resource_key: string;
  progress: number | null;
  processed_items: number | null;
  total_items: number | null;
  speed: number | null;
  eta_seconds: number | null;
  last_error_code: string;
  last_error_message: string;
  next_attempt_at: string | null;
  cancellation_requested: boolean;
  interrupt_pending: string;
  attempt_count: number;
  created_at: string;
  updated_at: string;
  started_at: string | null;
  finished_at: string | null;
  action_request: JobAction | null;
}
export interface JobAttempt {
  id: number;
  attempt_number: number;
  status: string;
  error_code: string;
  error_message: string;
  started_at: string;
  finished_at: string | null;
}
export interface JobEvent {
  id: number;
  event_type: string;
  from_status: string;
  to_status: string;
  actor_id: number | null;
  code: string;
  created_at: string;
}
export interface Page<T> {
  list: T[];
  total: number;
  page: number;
  page_size: number;
}
export const statusLabels: Record<JobStatus, string> = {
  queued: "排队中",
  running: "运行中",
  waiting_user_action: "等待操作",
  retry_wait: "等待重试",
  paused: "已暂停",
  completed: "已完成",
  failed: "失败",
  cancelled: "已取消",
};
export const listJobs = (query: URLSearchParams) =>
  api<Page<Job>>(`/api/v1/jobs?${query}`);
export const getAttempts = (id: string) =>
  api<{ list: JobAttempt[] }>(`/api/v1/jobs/${id}/attempts`);
export const getTimeline = (id: string) =>
  api<{ list: JobEvent[] }>(`/api/v1/jobs/${id}/timeline`);
export const controlJob = (
  id: string,
  action: "pause" | "resume" | "cancel" | "retry",
) => api<Job>(`/api/v1/jobs/${id}/${action}`, { method: "POST", body: "{}" });
export const respondAction = (job: Job, response: string) =>
  api<Job>(
    `/api/v1/jobs/${job.id}/actions/${job.action_request?.version}/respond`,
    { method: "POST", body: JSON.stringify({ response }) },
  );
export const reorderLane = (jobType: string, priority: number, jobs: Job[]) =>
  api<{ list: Job[] }>(
    `/api/v1/job-lanes/${encodeURIComponent(jobType)}/${priority}/order`,
    {
      method: "PUT",
      body: JSON.stringify({
        jobs: jobs.map((job) => ({ id: job.id, revision: job.revision })),
      }),
    },
  );
export const unknown = (value: number | null | undefined, suffix = "") =>
  value === null || value === undefined ? "未知" : `${value}${suffix}`;
export const getJob = (id: string) => api<Job>(`/api/v1/jobs/${id}`);
