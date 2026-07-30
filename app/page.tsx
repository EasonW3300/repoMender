import { RepoMenderApp } from "./components/RepoMenderApp";

// RepoMenderApp owns the interactive shell so the root route and catch-all product
// routes render the same navigation, state model, and visual system.

export default function Home() {
  return <RepoMenderApp />;
}
