import {
  createContext,
  useContext,
  useState,
  type Dispatch,
  type ReactNode,
  type SetStateAction,
} from "react";
import type { MigrationDraft } from "./contracts";
const initial: MigrationDraft = {
  targetId: "db-uat-finance",
  schema: "CORE_BANKING",
  account: "DEPLOY_ADMIN",
  files: [],
  fixture: false,
};
const DraftContext = createContext<{
  draft: MigrationDraft;
  setDraft: Dispatch<SetStateAction<MigrationDraft>>;
}>({ draft: initial, setDraft: () => {} });
export function DraftProvider({ children }: { children: ReactNode }) {
  const [draft, setDraft] = useState(initial);
  return (
    <DraftContext.Provider value={{ draft, setDraft }}>
      {children}
    </DraftContext.Provider>
  );
}
// eslint-disable-next-line react-refresh/only-export-components
export const useDraft = () => useContext(DraftContext);
