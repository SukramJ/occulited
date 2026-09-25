import {describe, expect, it} from 'vitest';
import {withTicket} from './download';

// task 125: the ticket rides in the query, whatever the link carries
describe('withTicket', () => {
    it('appends the ticket to a bare path and to one with a query', () => {
        expect(withTicket('/api/system/v1/backup', 'TICKETAAAAAAAAAAAAAAAAAAAA')).toBe('/api/system/v1/backup?ticket=TICKETAAAAAAAAAAAAAAAAAAAA');
        expect(withTicket('/api/system/v1/log/download?unit=rfd&format=json', 'T')).toBe('/api/system/v1/log/download?unit=rfd&format=json&ticket=T');
    });
    it('never carries a session', () => {
        expect(withTicket('/api/system/v1/backup?sid=@x@', 'T')).not.toContain('T@');
        expect(withTicket('/api/system/v1/backup', 'T')).not.toContain('sid=');
    });
});
