export interface ProjectMember {
  target_id: string;
  schema_id?: string;
}
export interface Project {
  id: string;
  name: string;
  description: string;
  status: "active" | "archived";
  created_at: string;
  updated_at: string;
  members: ProjectMember[];
}
export type ProjectInput = Pick<
  Project,
  "name" | "description" | "status" | "members"
>;
