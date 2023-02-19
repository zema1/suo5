export namespace ctrl {
	
	export class Suo5Config {
	    listen: string;
	    target: string;
	    no_auth: boolean;
	    username: string;
	    password: string;
	    mode: string;
	    ua: string;
	    buffer_size: number;
	    timeout: number;
	    debug: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Suo5Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.listen = source["listen"];
	        this.target = source["target"];
	        this.no_auth = source["no_auth"];
	        this.username = source["username"];
	        this.password = source["password"];
	        this.mode = source["mode"];
	        this.ua = source["ua"];
	        this.buffer_size = source["buffer_size"];
	        this.timeout = source["timeout"];
	        this.debug = source["debug"];
	    }
	}

}

export namespace main {
	
	export class Status {
	    connection_count: number;
	    memory_usage: string;
	    cpu_percent: string;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connection_count = source["connection_count"];
	        this.memory_usage = source["memory_usage"];
	        this.cpu_percent = source["cpu_percent"];
	    }
	}

}

