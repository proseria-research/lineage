import { useEffect, useState } from "react";
import { api } from "@/lib/api";

// Where people reach this registry from their own code. Fetched once per page load and shared.
// Without a configured public address, assume the host the console is served from, on the
// Model API's port.

export interface ClientInfo {
  modelApi: string;
  docs: string;
  /** Whether the address was configured or guessed, so the UI can say so. */
  guessed: boolean;
}

let cached: Promise<ClientInfo> | undefined;

function load(): Promise<ClientInfo> {
  cached ??= api
    .config()
    .catch(() => ({}) as Awaited<ReturnType<typeof api.config>>)
    .then((c) => ({
      modelApi: c.modelApiUrl || `${location.protocol}//${location.hostname}:${c.modelApiPort || "8081"}`,
      docs: c.docsUrl || "https://lineage.proseria.dev",
      guessed: !c.modelApiUrl,
    }));
  return cached;
}

export function useClientConfig(): ClientInfo | undefined {
  const [info, setInfo] = useState<ClientInfo>();
  useEffect(() => {
    let alive = true;
    load().then((i) => alive && setInfo(i));
    return () => {
      alive = false;
    };
  }, []);
  return info;
}
