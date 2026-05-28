export type MediaUrlResolver = (sessionId: string, eventSeq: number, mediaId: string, download?: boolean) => string;

export type MediaItem = {
  id: string;
  kind: string;
  path: string;
  name: string;
  mimeType: string;
  size?: number;
  status?: string;
  revisedPrompt?: string;
};
