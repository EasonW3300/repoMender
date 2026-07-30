import { RepoMenderApp } from "../components/RepoMenderApp";

// The catch-all route keeps every non-root product URL directly addressable while
// sharing the same application shell and mocked domain state during the UI phase.

export default function RepoMenderRoute() {
  return <RepoMenderApp />;
}
