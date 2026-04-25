"use client";

import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { DataTable } from "@/components/ui/data-table";
import { PageHeader } from "@/components/app/page-header";
import { useAdminJobs, useAdminMutation } from "@/lib/admin/client";
import { formatDateTime } from "@/lib/format";

type AdminJob = NonNullable<
  ReturnType<typeof useAdminJobs>["data"]
>["data"][number];

export default function AdminJobsPage() {
  const [state, setState] = useState("");
  const [kind, setKind] = useState("");
  const q = useAdminJobs({ state, kind });
  const retry = useAdminMutation((id) => `/api/v1/admin/jobs/${id}/retry`);
  return (
    <section className="flex flex-col gap-5">
      <PageHeader
        title="Jobs"
        description="River queue inspection and retry."
      />
      <div className="flex max-w-2xl gap-3">
        <Input
          value={state}
          onChange={(e) => setState(e.target.value)}
          placeholder="State"
        />
        <Input
          value={kind}
          onChange={(e) => setKind(e.target.value)}
          placeholder="Kind"
        />
      </div>
      <DataTable<AdminJob>
        columns={[
          { header: "Kind", cell: (j) => <span className="font-medium">{j.kind}</span> },
          { header: "Queue", cell: (j) => j.queue },
          {
            header: "State",
            cell: (j) => (
              <Badge
                variant={
                  j.state === "completed"
                    ? "success"
                    : j.state === "discarded"
                      ? "danger"
                      : "neutral"
                }
              >
                {j.state}
              </Badge>
            ),
          },
          { header: "Scheduled", cell: (j) => formatDateTime(j.scheduledAt) },
          {
            header: "",
            align: "right",
            cell: (j) => (
              <Button
                variant="secondary"
                size="sm"
                onClick={() => retry.mutate(String(j.id))}
              >
                Retry
              </Button>
            ),
          },
        ]}
        rows={q.data?.data ?? []}
        rowKey={(j) => j.id}
        isLoading={q.isLoading}
      />
    </section>
  );
}
