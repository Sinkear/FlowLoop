import { redirect } from "next/navigation";

// 新建工作流已迁到 /workflows/new，且必须带归属小队。
// 旧深链上的 ?squad= / ?template= 一并带过去，避免落到「无小队」的禁用态。
export default async function BlueprintNewRedirect({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = await searchParams;
  const qs = new URLSearchParams();
  for (const key of ["squad", "template"]) {
    const value = params[key];
    if (typeof value === "string" && value) qs.set(key, value);
  }
  const suffix = qs.toString();
  redirect(`/workflows/new${suffix ? `?${suffix}` : ""}`);
}
