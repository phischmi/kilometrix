export namespace main {
	
	export class Env {
	    binary: string;
	    workDir: string;
	    binOK: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Env(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.binary = source["binary"];
	        this.workDir = source["workDir"];
	        this.binOK = source["binOK"];
	    }
	}
	export class Health {
	    online: boolean;
	    engineReady: boolean;
	    geocodeReady: boolean;
	    authRequired: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Health(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.online = source["online"];
	        this.engineReady = source["engineReady"];
	        this.geocodeReady = source["geocodeReady"];
	        this.authRequired = source["authRequired"];
	    }
	}

}

