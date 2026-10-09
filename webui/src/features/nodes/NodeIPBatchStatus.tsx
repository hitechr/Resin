import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RefreshCw, X } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "../../components/ui/Button";
import { useI18n } from "../../i18n";
import { ApiError } from "../../lib/api-client";
import { formatApiErrorMessage } from "../../lib/error-message";
import { cancelNodeIPBatch, getNodeIPBatch, startNodeIPBatch } from "./api";
import type { NodeListFilters } from "./types";

export function NodeIPBatchStatus({ filters, enabled, estimated }: { filters: NodeListFilters; enabled: boolean; estimated: number }) {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const [jobID, setJobID] = useState("");
  const start = useMutation({
    mutationFn: () => startNodeIPBatch(filters),
    onSuccess: (job) => {
      setJobID(job.id);
      queryClient.setQueryData(["node-ip-batch", job.id], job);
    },
  });
  const status = useQuery({
    queryKey: ["node-ip-batch", jobID],
    queryFn: () => getNodeIPBatch(jobID),
    enabled: Boolean(jobID),
    retry: false,
    refetchInterval: (query) => {
      if (query.state.data?.ended_at) return false;
      const error = query.state.error;
      if (error instanceof ApiError && error.code === "IP_QUALITY_JOB_NOT_FOUND") return false;
      return error ? 3_000 : 1_000;
    },
  });
  const cancel = useMutation({
    mutationFn: () => cancelNodeIPBatch(jobID),
    onSuccess: (job) => queryClient.setQueryData(["node-ip-batch", job.id], job),
  });
  useEffect(() => {
    if (status.data?.ended_at) {
      void queryClient.invalidateQueries({ queryKey: ["node-ip-groups"] });
      void queryClient.invalidateQueries({ queryKey: ["node-ip-group"] });
    }
  }, [queryClient, status.data?.ended_at]);

  const job = status.data;
  const error = start.error || cancel.error || status.error;
  const lost = status.error instanceof ApiError && status.error.code === "IP_QUALITY_JOB_NOT_FOUND";
  const knownError = error instanceof ApiError ? ({
    IP_QUALITY_BATCH_TOO_LARGE: t("超过单次检查的 1000 个 IP 上限"),
    IP_QUALITY_QUEUE_FULL: t("检查队列已满，请稍后重试"),
    IP_QUALITY_DISABLED: t("IP 质量检查已关闭"),
    IP_QUALITY_STOPPED: t("检查服务正在停止"),
  } as Record<string, string>)[error.code] : undefined;
  const progress = job ? job.completed + job.failed + job.fresh_skipped + job.deferred : 0;
  return (
    <div className="ip-batch-status">
      <Button
        variant="secondary"
        size="sm"
        disabled={!enabled || start.isPending || (Boolean(jobID) && !job?.ended_at && !lost)}
        onClick={() => start.mutate()}
        title={t("批量检查当前筛选的 IP")}
      >
        <RefreshCw size={15} className={start.isPending ? "spin" : undefined} />
        {t("批量检查 IP")} ({estimated})
      </Button>
      {job ? (
        <span className="ip-batch-progress" role="status">
          {t("已处理 {{done}} / {{total}} 个 IP", { done: progress, total: job.total })}
          {job.failed ? ` · ${t("失败 {{count}}", { count: job.failed })}${job.error_summary ? ` (${job.error_summary === "invalid provider response" ? t("质量服务返回无效数据") : t("质量服务暂时不可用")})` : ""}` : ""}
          {job.fresh_skipped ? ` · ${t("已跳过新鲜缓存 {{count}}", { count: job.fresh_skipped })}` : ""}
          {job.deferred ? ` · ${t("未执行 {{count}}", { count: job.deferred })}` : ""}
          {job.canceled ? ` · ${t("已取消")}` : ""}
        </span>
      ) : null}
      {job && !job.ended_at ? <Button variant="ghost" size="sm" title={t("取消批量检查")} aria-label={t("取消批量检查")} disabled={cancel.isPending} onClick={() => cancel.mutate()}><X size={15} /></Button> : null}
      {error ? <span className="ip-batch-error" role="alert">{lost ? t("任务状态已丢失，请重新发起") : knownError ?? formatApiErrorMessage(error, t)}</span> : null}
    </div>
  );
}
