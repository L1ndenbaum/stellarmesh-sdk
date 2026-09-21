import type { SseMessage } from './contracts';

/** 增量解析行与事件，既不裁剪业务数据，也不将 EOF 当作事件分隔符。 */
export function createSseParser() {
  let line = '';
  let skipLF = false;
  let data: string[] = [];
  let event = '';
  let id = '';
  let retry: number | undefined;

  function consumeLine(): SseMessage | undefined {
    const value = line;
    line = '';
    if (value === '') {
      const message = data.length
        ? { data: data.join('\n'), event: event || 'message', id, retry }
        : undefined;
      data = [];
      event = '';
      return message;
    }
    const colon = value.indexOf(':');
    const field = colon < 0 ? value : value.slice(0, colon);
    let content = colon < 0 ? '' : value.slice(colon + 1);
    if (content.startsWith(' ')) content = content.slice(1);
    switch (field) {
      case 'data':
        data.push(content);
        break;
      case 'event':
        event = content;
        break;
      case 'id':
        if (!content.includes('\0')) id = content;
        break;
      case 'retry':
        if (/^[0-9]+$/.test(content) && Number.isSafeInteger(Number(content)))
          retry = Number(content);
        break;
    }
  }

  return function* parse(chunk: string): Generator<SseMessage> {
    for (const character of chunk) {
      if (skipLF) {
        skipLF = false;
        if (character === '\n') continue;
      }
      if (character === '\r' || character === '\n') {
        skipLF = character === '\r';
        const message = consumeLine();
        if (message) yield message;
      } else {
        line += character;
      }
    }
  };
}
